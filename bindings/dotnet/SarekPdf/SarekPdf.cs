// .NET bindings for sarek_pdf (P/Invoke over the C ABI; no dependencies).
//
// The native library "sarekpdf" (libsarekpdf.so / libsarekpdf.dylib /
// sarekpdf.dll) must be resolvable by the runtime, e.g. placed next to the
// application or under runtimes/<rid>/native in a NuGet package.
//
//   using var doc = SarekPdf.Document.Open("report.pdf");
//   Console.WriteLine(doc.PageCount);
//   foreach (var hit in doc.Search("revenue")) Console.WriteLine(hit.Page);
//   File.WriteAllBytes("report.docx", doc.ToDocx());
//
// A Document is safe to use from multiple threads.

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace SarekPdf
{
    /// <summary>Error reported by the native library.</summary>
    public sealed class SarekPdfException : Exception
    {
        public SarekPdfException(string message) : base(message) { }
    }

    public sealed record Rect(
        [property: JsonPropertyName("X0")] double X0, [property: JsonPropertyName("Y0")] double Y0,
        [property: JsonPropertyName("X1")] double X1, [property: JsonPropertyName("Y1")] double Y1);

    public sealed record SearchHit(
        [property: JsonPropertyName("page")] int Page,
        [property: JsonPropertyName("text")] string Text,
        [property: JsonPropertyName("rects")] List<Rect> Rects);

    public sealed record Heading(
        [property: JsonPropertyName("level")] int Level,
        [property: JsonPropertyName("text")] string Text,
        [property: JsonPropertyName("page")] int Page,
        [property: JsonPropertyName("bbox")] Rect BBox);

    public sealed record Paragraph(
        [property: JsonPropertyName("page")] int Page,
        [property: JsonPropertyName("role")] string Role,
        [property: JsonPropertyName("level")] int Level,
        [property: JsonPropertyName("text")] string Text,
        [property: JsonPropertyName("original")] string? Original,
        [property: JsonPropertyName("bbox")] Rect BBox);

    /// <summary>Options used when opening a document.</summary>
    public sealed class OpenOptions
    {
        [JsonPropertyName("password")] public string Password { get; set; } = "";
        [JsonPropertyName("workers")] public int Workers { get; set; }
        [JsonPropertyName("tables")] public bool? Tables { get; set; }
        [JsonPropertyName("drawings")] public bool? Drawings { get; set; }
        [JsonPropertyName("images")] public bool? Images { get; set; }
    }

    /// <summary>Search options.</summary>
    public sealed class SearchOptions
    {
        [JsonPropertyName("query")] public string Query { get; set; } = "";
        [JsonPropertyName("regex")] public bool Regex { get; set; }
        [JsonPropertyName("case_sensitive")] public bool CaseSensitive { get; set; }
        [JsonPropertyName("whole_word")] public bool WholeWord { get; set; }
        [JsonPropertyName("pages")] public string Pages { get; set; } = "";
        [JsonPropertyName("max_results")] public int MaxResults { get; set; }
        [JsonPropertyName("color")] public string? Color { get; set; }
    }

    internal static class Native
    {
        private const string Lib = "sarekpdf";

        [DllImport(Lib, EntryPoint = "sarek_version")] internal static extern IntPtr Version();
        [DllImport(Lib, EntryPoint = "sarek_free")] internal static extern void Free(IntPtr p);
        [DllImport(Lib, EntryPoint = "sarek_open_file")]
        internal static extern int OpenFile(byte[] path, byte[] opts, out ulong handle, out IntPtr err);
        [DllImport(Lib, EntryPoint = "sarek_open_bytes")]
        internal static extern int OpenBytes(byte[] data, UIntPtr len, byte[] opts, out ulong handle, out IntPtr err);
        [DllImport(Lib, EntryPoint = "sarek_open_url")]
        internal static extern int OpenUrl(byte[] url, byte[] opts, out ulong handle, out IntPtr err);
        [DllImport(Lib, EntryPoint = "sarek_close")] internal static extern void Close(ulong handle);
        [DllImport(Lib, EntryPoint = "sarek_call")]
        internal static extern int Call(ulong handle, byte[] op, byte[] args, out IntPtr output, out UIntPtr len, out IntPtr err);

        internal static byte[] Z(string s) => Encoding.UTF8.GetBytes(s + "\0");

        internal static SarekPdfException Error(IntPtr err)
        {
            var msg = err == IntPtr.Zero ? "unknown error" : Marshal.PtrToStringUTF8(err) ?? "unknown error";
            if (err != IntPtr.Zero) Free(err);
            return new SarekPdfException(msg);
        }
    }

    /// <summary>An opened PDF document. Dispose to release native memory.</summary>
    public sealed class Document : IDisposable
    {
        private ulong _handle;
        private static readonly JsonSerializerOptions Json = new() { DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull };

        private Document(ulong handle) => _handle = handle;

        public static string Version => Marshal.PtrToStringUTF8(Native.Version()) ?? "";

        /// <summary>Open a file path or an http(s) URL.</summary>
        public static Document Open(string pathOrUrl, OpenOptions? options = null)
        {
            var o = Native.Z(JsonSerializer.Serialize(options ?? new OpenOptions(), Json));
            bool url = pathOrUrl.StartsWith("http://", StringComparison.OrdinalIgnoreCase) ||
                       pathOrUrl.StartsWith("https://", StringComparison.OrdinalIgnoreCase);
            int rc = url ? Native.OpenUrl(Native.Z(pathOrUrl), o, out var h, out var err)
                         : Native.OpenFile(Native.Z(pathOrUrl), o, out h, out err);
            if (rc != 0) throw Native.Error(err);
            return new Document(h);
        }

        /// <summary>Open a PDF held in memory (the bytes are copied).</summary>
        public static Document FromBytes(byte[] data, OpenOptions? options = null)
        {
            var o = Native.Z(JsonSerializer.Serialize(options ?? new OpenOptions(), Json));
            if (Native.OpenBytes(data, (UIntPtr)data.Length, o, out var h, out var err) != 0) throw Native.Error(err);
            return new Document(h);
        }

        private byte[] Call(string op, object? args = null)
        {
            if (_handle == 0) throw new ObjectDisposedException(nameof(Document));
            var a = Native.Z(args == null ? "{}" : JsonSerializer.Serialize(args, Json));
            if (Native.Call(_handle, Native.Z(op), a, out var p, out var len, out var err) != 0) throw Native.Error(err);
            try
            {
                var buf = new byte[(int)len];
                if (buf.Length > 0) Marshal.Copy(p, buf, 0, buf.Length);
                return buf;
            }
            finally { Native.Free(p); }
        }

        private T CallJson<T>(string op, object? args = null) =>
            JsonSerializer.Deserialize<T>(Call(op, args))!;

        public int PageCount => JsonDocument.Parse(Call("info")).RootElement.GetProperty("pages").GetInt32();
        public string InfoJson() => Encoding.UTF8.GetString(Call("info"));
        public string Text(string pages = "") => Encoding.UTF8.GetString(Call("text", new { pages }));
        public string ToXml(string pages = "", bool geometry = true, bool embedImages = false) =>
            Encoding.UTF8.GetString(Call("xml", new { pages, geometry, embed_images = embedImages }));
        public byte[] ToDocx(string pages = "") => Call("docx", new { pages });
        public List<Paragraph> Paragraphs(string pages = "") => CallJson<List<Paragraph>>("paragraphs", new { pages });
        public List<Heading> Headings(string pages = "") => CallJson<List<Heading>>("headings", new { pages });
        public List<SearchHit> Search(string query) => Search(new SearchOptions { Query = query });
        public List<SearchHit> Search(SearchOptions o) => CallJson<List<SearchHit>>("search", o);
        /// <summary>Returns a copy of the PDF with all matches highlighted.</summary>
        public byte[] Highlight(SearchOptions o) => Call("highlight", o);
        /// <summary>Returns a new PDF containing the selected pages, e.g. "1-3,7".</summary>
        public byte[] ExtractPages(string pages) => Call("extract_pages", new { pages });

        public void Dispose()
        {
            if (_handle != 0) { Native.Close(_handle); _handle = 0; }
            GC.SuppressFinalize(this);
        }

        ~Document() { if (_handle != 0) Native.Close(_handle); }
    }
}

"""Python bindings for sarek_pdf (ctypes; no third-party dependencies).

The native library is located via, in order:
  1. the SAREK_PDF_LIB environment variable (full path),
  2. a libsarekpdf.{so,dylib} / sarekpdf.dll next to this file.

Example::

    from sarek_pdf import Document

    with Document.open("report.pdf") as doc:
        print(doc.page_count)
        print(doc.text(pages="1-2"))
        for hit in doc.search("revenue"):
            print(hit["page"], hit["rects"])
        doc.save_docx("report.docx")

A Document may be used from multiple threads; the native calls release
the GIL while they run.
"""

from __future__ import annotations

import ctypes
import json
import os
import sys
from typing import Any, Dict, List, Optional

__all__ = ["Document", "SarekError", "version"]


class SarekError(RuntimeError):
    """Raised when the native library reports an error."""


def _lib_path() -> str:
    env = os.environ.get("SAREK_PDF_LIB")
    if env:
        return env
    here = os.path.dirname(os.path.abspath(__file__))
    name = {"win32": "sarekpdf.dll", "darwin": "libsarekpdf.dylib"}.get(sys.platform, "libsarekpdf.so")
    return os.path.join(here, name)


_lib = ctypes.CDLL(_lib_path())
_u64p = ctypes.POINTER(ctypes.c_uint64)
_errp = ctypes.POINTER(ctypes.c_void_p)

_lib.sarek_version.restype = ctypes.c_char_p
_lib.sarek_free.argtypes = [ctypes.c_void_p]
_lib.sarek_open_file.argtypes = [ctypes.c_char_p, ctypes.c_char_p, _u64p, _errp]
_lib.sarek_open_bytes.argtypes = [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_char_p, _u64p, _errp]
_lib.sarek_open_url.argtypes = [ctypes.c_char_p, ctypes.c_char_p, _u64p, _errp]
_lib.sarek_close.argtypes = [ctypes.c_uint64]
_lib.sarek_call.argtypes = [ctypes.c_uint64, ctypes.c_char_p, ctypes.c_char_p,
                            ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(ctypes.c_size_t), _errp]
for _f in (_lib.sarek_open_file, _lib.sarek_open_bytes, _lib.sarek_open_url, _lib.sarek_call):
    _f.restype = ctypes.c_int


def version() -> str:
    return _lib.sarek_version().decode()


def _raise(err: ctypes.c_void_p) -> None:
    msg = ctypes.string_at(err.value).decode("utf-8", "replace") if err.value else "unknown error"
    if err.value:
        _lib.sarek_free(err)
    raise SarekError(msg)


def _opts(**kw: Any) -> bytes:
    return json.dumps({k: v for k, v in kw.items() if v is not None}).encode()


class Document:
    """An opened PDF document. Close it (or use ``with``) to free memory."""

    def __init__(self, handle: int):
        self._h = handle

    # -- opening -----------------------------------------------------------
    @classmethod
    def open(cls, path: str, *, password: str = "", workers: int = 0,
             tables: Optional[bool] = None, drawings: Optional[bool] = None,
             images: Optional[bool] = None, llm: Optional[Dict[str, Any]] = None) -> "Document":
        """Open a file path, or an http(s) URL."""
        h, err = ctypes.c_uint64(), ctypes.c_void_p()
        o = _opts(password=password, workers=workers, tables=tables, drawings=drawings, images=images, llm=llm)
        fn = _lib.sarek_open_url if path.startswith(("http://", "https://")) else _lib.sarek_open_file
        if fn(path.encode(), o, ctypes.byref(h), ctypes.byref(err)) != 0:
            _raise(err)
        return cls(h.value)

    @classmethod
    def from_bytes(cls, data: bytes, **kw: Any) -> "Document":
        """Open a PDF held in memory (the bytes are copied)."""
        h, err = ctypes.c_uint64(), ctypes.c_void_p()
        buf = ctypes.create_string_buffer(data, len(data))
        if _lib.sarek_open_bytes(buf, len(data), _opts(**kw), ctypes.byref(h), ctypes.byref(err)) != 0:
            _raise(err)
        return cls(h.value)

    def close(self) -> None:
        if self._h:
            _lib.sarek_close(self._h)
            self._h = 0

    def __enter__(self) -> "Document":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def __del__(self) -> None:  # best effort
        try:
            self.close()
        except Exception:
            pass

    # -- calls -------------------------------------------------------------
    def _call(self, op: str, **args: Any) -> bytes:
        if not self._h:
            raise SarekError("document is closed")
        out, n, err = ctypes.c_void_p(), ctypes.c_size_t(), ctypes.c_void_p()
        rc = _lib.sarek_call(self._h, op.encode(), _opts(**args), ctypes.byref(out), ctypes.byref(n), ctypes.byref(err))
        if rc != 0:
            _raise(err)
        try:
            return ctypes.string_at(out.value, n.value)
        finally:
            _lib.sarek_free(out)

    def info(self) -> Dict[str, Any]:
        return json.loads(self._call("info"))

    @property
    def page_count(self) -> int:
        return int(self.info()["pages"])

    def text(self, pages: str = "") -> str:
        return self._call("text", pages=pages).decode()

    def paragraphs(self, pages: str = "") -> List[Dict[str, Any]]:
        return json.loads(self._call("paragraphs", pages=pages))

    def headings(self, pages: str = "") -> List[Dict[str, Any]]:
        return json.loads(self._call("headings", pages=pages))

    def search(self, query: str, *, regex: bool = False, case_sensitive: bool = False,
               whole_word: bool = False, pages: str = "", max_results: int = 0) -> List[Dict[str, Any]]:
        return json.loads(self._call("search", query=query, regex=regex, case_sensitive=case_sensitive,
                                     whole_word=whole_word, pages=pages, max_results=max_results))

    def to_xml(self, pages: str = "", geometry: bool = True, embed_images: bool = False) -> str:
        return self._call("xml", pages=pages, geometry=geometry, embed_images=embed_images).decode()

    def to_docx(self, pages: str = "") -> bytes:
        return self._call("docx", pages=pages)

    def save_docx(self, path: str, pages: str = "") -> None:
        with open(path, "wb") as f:
            f.write(self.to_docx(pages))

    def highlight(self, query: str, out_path: str, *, color: str = "FFEB3B", **search: Any) -> None:
        """Write a copy of the PDF with all matches of ``query`` highlighted."""
        data = self._call("highlight", query=query, color=color, **search)
        with open(out_path, "wb") as f:
            f.write(data)

    def extract_pages(self, pages: str, out_path: str) -> None:
        """Write the selected pages (e.g. "1-3,7") to a new PDF."""
        with open(out_path, "wb") as f:
            f.write(self._call("extract_pages", pages=pages))

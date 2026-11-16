// Command sarekpdf is a command-line front end for the sarek_pdf library.
//
//	sarekpdf info      [flags] <file|url>
//	sarekpdf convert   [flags] -f docx|xml|text -o out <file|url>
//	sarekpdf search    [flags] -q query [-regex] <file|url>
//	sarekpdf highlight [flags] -q query -o out.pdf <file|url>
//	sarekpdf pages     [flags] -p 1-3,7 -o out.pdf <file|url>
//	sarekpdf headings  [flags] <file|url>
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	sarekpdf "github.com/khanshahan/sarek_pdf"
	"github.com/khanshahan/sarek_pdf/correct/llm"
	"github.com/khanshahan/sarek_pdf/correct/llm/openai"
	"github.com/khanshahan/sarek_pdf/export/docx"
	"github.com/khanshahan/sarek_pdf/export/xmlexport"
	"github.com/khanshahan/sarek_pdf/model"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: sarekpdf <command> [flags] <file|url>

commands:
  info       page count, version, metadata, outline
  convert    export to docx, xml or text (-f, -o)
  search     find text (-q, -regex, -case, -word)
  highlight  write a copy with search hits highlighted (-q, -o)
  pages      extract pages into a new PDF (-p, -o)
  headings   list headings detected from the layout

run "sarekpdf <command> -h" for flags`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	password := fs.String("password", "", "document password")
	workers := fs.Int("workers", 0, "parallel pages (default: all CPUs)")
	pages := fs.String("p", "", "page ranges, e.g. 1-3,7 (default: all)")
	out := fs.String("o", "", "output file (default: stdout)")
	format := fs.String("f", "text", "convert format: docx, xml, text")
	query := fs.String("q", "", "search query")
	regex := fs.Bool("regex", false, "query is a regular expression")
	caseSens := fs.Bool("case", false, "case-sensitive search")
	word := fs.Bool("word", false, "whole-word search")
	llmURL := fs.String("llm-url", "", "OpenAI-compatible endpoint for constrained correction, e.g. http://127.0.0.1:8080/v1")
	llmModel := fs.String("llm-model", "", "model name for -llm-url")
	llmConc := fs.Int("llm-concurrency", 2, "parallel LLM requests")
	images := fs.Bool("images", true, "extract images")
	embed := fs.Bool("embed-images", false, "xml: embed image data as base64")
	geometry := fs.Bool("geometry", true, "xml: include line/span coordinates")
	timeout := fs.Duration("timeout", 0, "overall timeout (e.g. 2m)")
	fs.Parse(os.Args[2:])
	if fs.NArg() != 1 {
		usage()
	}
	src := fs.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	opts := []sarekpdf.Option{sarekpdf.WithPassword(*password), sarekpdf.WithWorkers(*workers), sarekpdf.WithImages(*images)}
	if *llmURL != "" {
		opts = append(opts, sarekpdf.WithCorrector(&llm.Corrector{Engine: openai.New(*llmURL, *llmModel), Concurrency: *llmConc}))
	}
	start := time.Now()
	var doc *sarekpdf.Document
	var err error
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		doc, err = sarekpdf.OpenURL(ctx, src, opts...)
	} else {
		doc, err = sarekpdf.Open(src, opts...)
	}
	check(err)
	defer doc.Close()
	sel, err := sarekpdf.ParsePageRanges(*pages, doc.NumPages())
	check(err)

	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		check(err)
		defer func() { check(f.Close()) }()
		w = f
	}

	switch cmd {
	case "info":
		info := map[string]any{
			"pages": doc.NumPages(), "version": doc.Version(), "encrypted": doc.Encrypted(),
			"metadata": doc.Metadata(), "outline": doc.Outline(),
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		check(enc.Encode(info))
	case "convert":
		res, err := doc.Extract(ctx, sel...)
		warn(err)
		if res == nil {
			os.Exit(1)
		}
		switch *format {
		case "docx":
			if *out == "" {
				die("convert -f docx requires -o")
			}
			check(docx.Write(w, res, docx.Options{}))
		case "xml":
			check(xmlexport.Write(w, res, xmlexport.Options{Geometry: *geometry, EmbedImages: *embed, Paths: true}))
		case "text":
			_, err := io.WriteString(w, res.Text()+"\n")
			check(err)
		default:
			die("unknown format " + *format)
		}
		fmt.Fprintf(os.Stderr, "%d pages in %v\n", len(res.Pages), time.Since(start).Round(time.Millisecond))
	case "search", "highlight":
		if *query == "" {
			die(cmd + " requires -q")
		}
		ms, err := doc.Search(ctx, *query, sarekpdf.SearchOptions{Regexp: *regex, CaseSensitive: *caseSens, WholeWord: *word, Pages: sel})
		warn(err)
		if cmd == "search" {
			for _, m := range ms {
				r := m.Rects[0]
				fmt.Fprintf(w, "page %d [%.1f %.1f %.1f %.1f]: %s\n", m.Page, r.X0, r.Y0, r.X1, r.Y1, m.Text)
			}
			fmt.Fprintf(os.Stderr, "%d matches\n", len(ms))
			return
		}
		if *out == "" {
			die("highlight requires -o")
		}
		check(doc.WriteHighlighted(w, sarekpdf.HighlightMatches(ms, model.Color{})))
		fmt.Fprintf(os.Stderr, "highlighted %d matches\n", len(ms))
	case "pages":
		if *out == "" || *pages == "" {
			die("pages requires -p and -o")
		}
		check(doc.ExtractPages(w, sel))
	case "headings":
		hs, err := doc.Headings(ctx, sel...)
		warn(err)
		for _, h := range hs {
			fmt.Fprintf(w, "%s%s  (page %d)\n", strings.Repeat("  ", max(h.Level-1, 0)), h.Text, h.Page)
		}
	default:
		usage()
	}
}

func check(err error) {
	if err != nil {
		die(err.Error())
	}
}

func warn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "sarekpdf:", msg)
	os.Exit(1)
}

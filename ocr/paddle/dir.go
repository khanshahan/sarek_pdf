package paddle

import "path/filepath"

// FromDir builds Options for the directory layout of the
// "monkt/paddleocr-onnx" model collection:
//
//	<dir>/detection/v5/det.onnx
//	<dir>/languages/<lang>/rec.onnx
//	<dir>/languages/<lang>/dict.txt
//
// lang is e.g. "english", "latin", "eslav", "greek", "korean", "chinese".
func FromDir(dir, lang string) Options {
	if lang == "" {
		lang = "english"
	}
	return Options{
		DetModel: filepath.Join(dir, "detection", "v5", "det.onnx"),
		RecModel: filepath.Join(dir, "languages", lang, "rec.onnx"),
		Dict:     filepath.Join(dir, "languages", lang, "dict.txt"),
	}
}

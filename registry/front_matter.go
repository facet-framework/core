package facet

import (
	"bytes"
	"strings"
)

// frontMatterFence is the line that opens and closes a YAML header.
const frontMatterFence = "---"

// SplitFrontMatter separates a Markdown file's YAML header from its prose.
func SplitFrontMatter(data []byte) (front, body []byte, ok bool) {
	text := data
	// A file written on Windows opens with the same fence and a carriage
	// return; refusing it would make the header depend on the editor.
	rest, isFence := cutFence(text)
	if !isFence {
		return nil, data, false
	}
	for offset := 0; offset < len(rest); {
		line, next := lineAt(rest, offset)
		// Trailing blanks after the closing fence are allowed (`---[ \t]*`):
		// two grammars for one head would let a file one reader shows with a
		// head be written by another as one without.
		if strings.TrimRight(string(line), "\r \t") == frontMatterFence {
			return rest[:offset], dropSeparator(rest[next:]), true
		}
		offset = next
	}
	return nil, data, false
}

// dropSeparator removes the single blank line that conventionally sits between
// the closing fence and the prose.
func dropSeparator(body []byte) []byte {
	switch {
	case bytes.HasPrefix(body, []byte("\r\n")):
		return body[2:]
	case bytes.HasPrefix(body, []byte("\n")):
		return body[1:]
	}
	return body
}

// cutFence removes an opening header fence, reporting whether there was one.
func cutFence(data []byte) ([]byte, bool) {
	line, next := lineAt(data, 0)
	if strings.TrimRight(string(line), "\r") != frontMatterFence {
		return data, false
	}
	return data[next:], true
}

// lineAt returns the line beginning at offset and where the next one begins.
func lineAt(data []byte, offset int) (line []byte, next int) {
	end := bytes.IndexByte(data[offset:], '\n')
	if end < 0 {
		return data[offset:], len(data)
	}
	return data[offset : offset+end], offset + end + 1
}

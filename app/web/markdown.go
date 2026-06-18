package web

import "strings"

// markdownToTelegramHTML converts a small Markdown subset into the HTML subset
// that the upload caption pipeline understands (app/up/iter.go parses the
// caption with html.HTML):
//
//	`code`              -> <code>code</code>
//	[text](url)         -> <a href="url">text</a>
//	**bold**            -> <b>bold</b>
//	*italic* / _italic_ -> <i>italic</i>
//	~~strike~~          -> <s>strike</s>
//
// Everything else is literal text and HTML-escaped. Constructs are not nested
// (their inner text is only escaped), which keeps the scanner simple and
// predictable for a caption box.
func markdownToTelegramHTML(s string) string {
	var b strings.Builder
	r := []rune(s)
	n := len(r)

	// readUntil scans for the next literal occurrence of close starting at start,
	// returning the inner text, the index just past close, and whether it closed.
	readUntil := func(start int, close string) (string, int, bool) {
		cr := []rune(close)
		for j := start; j+len(cr) <= n; j++ {
			if string(r[j:j+len(cr)]) == close {
				return string(r[start:j]), j + len(cr), true
			}
		}
		return "", 0, false
	}

	emit := func(tag, inner string) {
		b.WriteString("<" + tag + ">")
		b.WriteString(escapeHTMLText(inner))
		b.WriteString("</" + tag + ">")
	}

	i := 0
	for i < n {
		c := r[i]
		switch {
		case c == '`':
			if inner, next, ok := readUntil(i+1, "`"); ok {
				emit("code", inner)
				i = next
				continue
			}
		case c == '[':
			if text, afterText, ok := readUntil(i+1, "]"); ok && afterText < n && r[afterText] == '(' {
				if url, afterURL, ok2 := readUntil(afterText+1, ")"); ok2 {
					b.WriteString(`<a href="`)
					b.WriteString(escapeHTMLAttr(strings.TrimSpace(url)))
					b.WriteString(`">`)
					b.WriteString(escapeHTMLText(text))
					b.WriteString("</a>")
					i = afterURL
					continue
				}
			}
		case c == '*' && i+1 < n && r[i+1] == '*':
			if inner, next, ok := readUntil(i+2, "**"); ok && inner != "" {
				emit("b", inner)
				i = next
				continue
			}
		case c == '~' && i+1 < n && r[i+1] == '~':
			if inner, next, ok := readUntil(i+2, "~~"); ok && inner != "" {
				emit("s", inner)
				i = next
				continue
			}
		case c == '*' || c == '_':
			if inner, next, ok := readUntil(i+1, string(c)); ok && inner != "" {
				emit("i", inner)
				i = next
				continue
			}
		}
		// literal character
		b.WriteString(escapeHTMLText(string(c)))
		i++
	}
	return b.String()
}

func escapeHTMLText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func escapeHTMLAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// exprStringLiteral wraps a literal string as an expr double-quoted string, so
// it passes through app/up's expr-based caption resolution unchanged (the
// resolved value is then HTML-parsed by the uploader). This lets the web send a
// plain/markdown caption without it being interpreted as an expr expression.
func exprStringLiteral(s string) string {
	repl := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", "",
		"\t", `\t`,
	)
	return `"` + repl.Replace(s) + `"`
}

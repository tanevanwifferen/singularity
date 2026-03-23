package components

import "unicode"

// DeleteWord removes the word before the cursor from text, returning the new
// text and cursor position. It mimics shell/readline ctrl+w behavior: skip
// trailing whitespace, then delete back to the next whitespace boundary.
func DeleteWord(text string, cursor int) (string, int) {
	if cursor <= 0 || len(text) == 0 {
		return text, cursor
	}
	i := cursor
	// Skip whitespace before cursor
	for i > 0 && unicode.IsSpace(rune(text[i-1])) {
		i--
	}
	// Delete back through non-whitespace
	for i > 0 && !unicode.IsSpace(rune(text[i-1])) {
		i--
	}
	return text[:i] + text[cursor:], i
}

// DeleteWordEnd removes the word before the end of text, returning the new
// text. This is a convenience for simple append-only inputs with no cursor.
func DeleteWordEnd(text string) string {
	result, _ := DeleteWord(text, len(text))
	return result
}

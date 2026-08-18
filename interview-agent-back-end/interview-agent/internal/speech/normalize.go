package speech

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	sourceMarkerPattern = regexp.MustCompile("(?s)\\s*`?\\s*\\[来源\\s*[:：][^\\]\\r\\n]*\\]\\s*`?\\s*$")
	fencedCodePattern   = regexp.MustCompile("(?s)(```.*?```|~~~.*?~~~)")
	markdownLinkPattern = regexp.MustCompile("!?\\[([^\\]]+)\\]\\([^\\)]+\\)")
	inlineCodePattern   = regexp.MustCompile("`([^`\\r\\n]+)`")
	headingPattern      = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	quotePattern        = regexp.MustCompile(`(?m)^\s*>\s?`)
	bulletPattern       = regexp.MustCompile(`(?m)^\s*[-+*]\s+`)
	orderedListPattern  = regexp.MustCompile(`(?m)^\s*([0-9]+)[.)]\s+`)
	boldStarPattern     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	boldUnderscore      = regexp.MustCompile(`__([^_]+)__`)
	italicStarPattern   = regexp.MustCompile(`\*([^*\r\n]+)\*`)
	strikePattern       = regexp.MustCompile(`~~([^~]+)~~`)
	rawURLPattern       = regexp.MustCompile(`https?://[^\s)]+`)
)

// NormalizeText converts display Markdown into a stable, readable TTS input
// without mutating the original question content.
func NormalizeText(input string, maxRunes int) (string, error) {
	text := sourceMarkerPattern.ReplaceAllString(input, "")
	text = fencedCodePattern.ReplaceAllString(text, " 请查看屏幕中的代码 ")
	text = markdownLinkPattern.ReplaceAllString(text, "$1")
	text = inlineCodePattern.ReplaceAllString(text, "$1")
	text = headingPattern.ReplaceAllString(text, "")
	text = quotePattern.ReplaceAllString(text, "")
	text = bulletPattern.ReplaceAllString(text, "")
	text = orderedListPattern.ReplaceAllString(text, "$1. ")
	text = boldStarPattern.ReplaceAllString(text, "$1")
	text = boldUnderscore.ReplaceAllString(text, "$1")
	text = italicStarPattern.ReplaceAllString(text, "$1")
	text = strikePattern.ReplaceAllString(text, "$1")
	text = rawURLPattern.ReplaceAllString(text, "")
	text = strings.Join(strings.Fields(text), " ")

	if text == "" {
		return "", ErrInvalidRequest
	}
	if maxRunes <= 0 {
		return "", ErrInvalidRequest
	}
	if utf8.RuneCountInString(text) > maxRunes {
		return "", ErrTextTooLong
	}
	return text, nil
}

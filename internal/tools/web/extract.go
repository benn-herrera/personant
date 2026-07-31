package web

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"golang.org/x/net/html"
)

// The HTML pipeline: parse → readability → Markdown.
//
// Readability strips the chrome (nav, ads, related-links rails, cookie
// banners, footers) so what reaches model context is the article rather
// than the site. The Markdown conversion preserves links, headings and
// list structure — which matters precisely because the agent may want to
// follow one of those links, and a link that survived as `[text](url)`
// is a link it can act on.

// Empty-shell thresholds. A client-rendered SPA answers a plain GET with
// a document that has a <div id="root"></div> and 400 KB of script tags,
// and readability faithfully extracts nothing from it.
//
// Returning that as content is the failure mode this exists to prevent:
// a model told "the page is blank" concludes the information does not
// exist, which is a WRONG and confident answer. Told "this page needs
// JavaScript", it tries another source. The second outcome is strictly
// better even when the detector is wrong, which is why the notice names
// both possible causes rather than asserting the SPA diagnosis.
const (
	// shellMinHTMLBytes is the floor below which "little text" is simply
	// a small page, not a shell. Without it, a legitimately terse
	// document would be reported as unavailable.
	shellMinHTMLBytes = 2048

	// shellMinTextChars is the extracted-text floor. Under this much
	// text, from a document over shellMinHTMLBytes, there is nothing
	// worth calling article content.
	shellMinTextChars = 200

	// shellRatioHTMLBytes / shellMinTextRatio catch the other shape: a
	// large document (inlined bundle, JSON state blob) carrying a
	// paragraph or two of real text. Ratio alone would misfire on small
	// documents, so it applies only above the size floor.
	shellRatioHTMLBytes = 50 << 10
	shellMinTextRatio   = 0.005
)

// article is the extraction result: the readable body plus the metadata
// a long-career memory system uses to judge whether what it just read is
// still current. Publication date and byline are not decoration here —
// "is this stale?" is a question personant asks constantly.
type article struct {
	title     string
	byline    string
	siteName  string
	language  string
	published string
	markdown  string
	textLen   int
	htmlLen   int
}

// renderArticle runs the HTML pipeline and formats the tool result.
func renderArticle(ctx context.Context, body []byte, final *url.URL) ([]byte, error) {
	art, err := extractArticle(ctx, body, final)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	meta := map[string]string{
		"url":       final.Redacted(),
		"title":     art.title,
		"byline":    art.byline,
		"site":      art.siteName,
		"published": art.published,
		"language":  art.language,
	}
	order := []string{"url", "title", "byline", "site", "published", "language"}

	// An empty body is reported as no-content whatever its size: handing
	// back a metadata header with nothing under it is the same lie the
	// shell detector exists to prevent, just at a smaller scale.
	if art.markdown == "" || looksEmptyShell(art.textLen, art.htmlLen) {
		meta["status"] = "no-content"
		writeMeta(&b, meta, append(order, "status")...)
		b.WriteString("\n")
		fmt.Fprintf(&b, emptyShellNotice, art.htmlLen, art.textLen)
		return b.Bytes(), nil
	}

	writeMeta(&b, meta, order...)
	b.WriteString("\n")
	b.WriteString(art.markdown)
	b.WriteString("\n")
	return b.Bytes(), nil
}

// emptyShellNotice is the model-facing signal. It states the measurement,
// names BOTH causes (client-side rendering or a genuinely text-less
// page), and says explicitly what not to conclude — because the wrong
// conclusion ("the page is blank") is the whole risk.
const emptyShellNotice = "This page requires JavaScript rendering; content unavailable. " +
	"web.fetch performs a plain HTTP GET with no browser, and this document " +
	"(%d bytes of HTML) yielded only %d characters of extractable text — either it renders " +
	"its content client-side, or it genuinely carries no article text.\n\n" +
	"Do NOT report that the page is empty or that the information does not exist: the fetch was " +
	"incomplete, not the page. Try a server-rendered alternative (a print/AMP view, an RSS or " +
	"API endpoint, a mirror), search for the content elsewhere, or ask the user.\n"

// extractArticle parses, extracts, and converts. It is separated from
// renderArticle so the pipeline can be measured directly in tests
// without reading formatting back out of a rendered blob.
func extractArticle(ctx context.Context, body []byte, pageURL *url.URL) (article, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return article{}, fmt.Errorf("could not parse the HTML at %s: %w", pageURL.Redacted(), err)
	}

	// FromDocument resolves relative hrefs/srcs against pageURL, so the
	// links that survive into Markdown are absolute and followable.
	parsed, err := readability.FromDocument(doc, pageURL)
	if err != nil {
		return article{}, fmt.Errorf("could not extract readable content from %s: %w", pageURL.Redacted(), err)
	}

	art := article{
		title:    parsed.Title(),
		byline:   parsed.Byline(),
		siteName: parsed.SiteName(),
		language: parsed.Language(),
		htmlLen:  len(body),
	}
	if t, err := parsed.PublishedTime(); err == nil {
		art.published = t.UTC().Format("2006-01-02")
	}

	// A nil Node is readability's own "nothing readable here" — the
	// strongest empty-shell signal there is, and it short-circuits both
	// the text render and the Markdown conversion.
	if parsed.Node == nil {
		return art, nil
	}

	var text strings.Builder
	if err := parsed.RenderText(&text); err != nil {
		return article{}, fmt.Errorf("could not render text from %s: %w", pageURL.Redacted(), err)
	}
	art.textLen = len(strings.TrimSpace(text.String()))

	md, err := htmltomarkdown.ConvertNode(parsed.Node, converter.WithContext(ctx))
	if err != nil {
		return article{}, fmt.Errorf("could not convert %s to Markdown: %w", pageURL.Redacted(), err)
	}
	art.markdown = strings.TrimSpace(string(md))
	return art, nil
}

// looksEmptyShell reports whether an extraction produced too little text
// to be believable content for a document of that size.
//
// FALSE-POSITIVE RISK, stated plainly: a real page that is short (a
// stub, a redirect notice, a link-only index) and carries more than
// shellMinHTMLBytes of markup is reported as unavailable. That is the
// deliberate direction of the error — an unnecessary "try another
// source" costs one wasted fetch, while the inverse error (an empty
// shell reported as content) produces a confidently wrong answer. The
// notice text names the "genuinely no article text" cause for exactly
// this case.
func looksEmptyShell(textLen, htmlLen int) bool {
	if htmlLen < shellMinHTMLBytes {
		return false
	}
	if textLen < shellMinTextChars {
		return true
	}
	return htmlLen >= shellRatioHTMLBytes && float64(textLen)/float64(htmlLen) < shellMinTextRatio
}

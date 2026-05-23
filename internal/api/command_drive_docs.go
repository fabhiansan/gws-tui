package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type rawDriveFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	ModifiedTime string   `json:"modifiedTime"`
	WebViewLink  string   `json:"webViewLink"`
	Size         string   `json:"size"`
	Parents      []string `json:"parents"`
}

type rawDocDocument struct {
	DocumentID    string                        `json:"documentId"`
	Title         string                        `json:"title"`
	Body          rawDocBody                    `json:"body"`
	InlineObjects map[string]rawDocInlineObject `json:"inlineObjects"`
}

type rawDocBody struct {
	Content []rawDocStructuralElement `json:"content"`
}

type rawDocStructuralElement struct {
	Paragraph *rawDocParagraph `json:"paragraph"`
	Table     *rawDocTable     `json:"table"`
}

type rawDocParagraph struct {
	Elements       []rawDocParagraphElement `json:"elements"`
	ParagraphStyle rawDocParagraphStyle     `json:"paragraphStyle"`
	Bullet         *rawDocBullet            `json:"bullet"`
}

type rawDocParagraphStyle struct {
	NamedStyleType string `json:"namedStyleType"`
}

type rawDocBullet struct {
	ListID       string `json:"listId"`
	NestingLevel int    `json:"nestingLevel"`
}

type rawDocParagraphElement struct {
	TextRun             *rawDocTextRun             `json:"textRun"`
	InlineObjectElement *rawDocInlineObjectElement `json:"inlineObjectElement"`
}

type rawDocTextRun struct {
	Content   string          `json:"content"`
	TextStyle rawDocTextStyle `json:"textStyle"`
}

type rawDocTextStyle struct {
	Bold          bool       `json:"bold"`
	Italic        bool       `json:"italic"`
	Underline     bool       `json:"underline"`
	Strikethrough bool       `json:"strikethrough"`
	Link          rawDocLink `json:"link"`
}

type rawDocLink struct {
	URL string `json:"url"`
}

type rawDocInlineObjectElement struct {
	InlineObjectID string `json:"inlineObjectId"`
}

type rawDocTable struct {
	TableRows []rawDocTableRow `json:"tableRows"`
}

type rawDocTableRow struct {
	TableCells []rawDocTableCell `json:"tableCells"`
}

type rawDocTableCell struct {
	Content []rawDocStructuralElement `json:"content"`
}

type rawDocInlineObject struct {
	ObjectID               string                       `json:"objectId"`
	InlineObjectProperties rawDocInlineObjectProperties `json:"inlineObjectProperties"`
}

type rawDocInlineObjectProperties struct {
	EmbeddedObject rawDocEmbeddedObject `json:"embeddedObject"`
}

type rawDocEmbeddedObject struct {
	Title           string                `json:"title"`
	Description     string                `json:"description"`
	ImageProperties rawDocImageProperties `json:"imageProperties"`
}

type rawDocImageProperties struct {
	ContentURI string `json:"contentUri"`
	SourceURI  string `json:"sourceUri"`
}

func (c *CommandClient) downloadDriveFile(ctx context.Context, fileID, outputPath string) error {
	if strings.TrimSpace(fileID) == "" {
		return errors.New("drive file id is required")
	}
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".gws-drive-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	params, _ := json.Marshal(map[string]string{"fileId": fileID, "alt": "media"})
	command := exec.CommandContext(ctx, c.path, "drive", "files", "get", "--params", string(params), "--output", filepath.Base(tmpPath))
	command.Dir = filepath.Dir(tmpPath)
	payload, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gws drive download failed: %s", strings.TrimSpace(string(payload)))
	}
	return os.Rename(tmpPath, outputPath)
}

func (c *CommandClient) DriveFiles(ctx context.Context, q DriveQuery) (Page[DriveFile], error) {
	return c.driveFiles(ctx, q)
}

func (c *CommandClient) Docs(ctx context.Context, q DriveQuery) (Page[DriveFile], error) {
	q.MimeType = "application/vnd.google-apps.document"
	return c.driveFiles(ctx, q)
}

func (c *CommandClient) driveFiles(ctx context.Context, q DriveQuery) (Page[DriveFile], error) {
	params := map[string]any{
		"pageSize": 50,
		"fields":   "nextPageToken, files(id,name,mimeType,modifiedTime,webViewLink,size,parents)",
	}
	var filters []string
	if strings.TrimSpace(q.Search) != "" {
		escaped := strings.ReplaceAll(q.Search, "'", "\\'")
		filters = append(filters, fmt.Sprintf("name contains '%s'", escaped))
	}
	if strings.TrimSpace(q.MimeType) != "" {
		filters = append(filters, fmt.Sprintf("mimeType = '%s'", q.MimeType))
	}
	if len(filters) > 0 {
		params["q"] = strings.Join(filters, " and ")
	}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Files         []rawDriveFile `json:"files"`
		Items         []rawDriveFile `json:"items"`
		NextPageToken string         `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "drive", "files", "list", "--params", string(payload), "--format", "json"); err != nil {
		return Page[DriveFile]{}, err
	}
	source := raw.Files
	if len(source) == 0 {
		source = raw.Items
	}
	items := make([]DriveFile, 0, len(source))
	for _, item := range source {
		size, _ := strconv.ParseInt(item.Size, 10, 64)
		items = append(items, DriveFile{
			ID:           item.ID,
			Name:         fallback(item.Name, "(untitled)"),
			MimeType:     item.MimeType,
			ModifiedTime: parseRFC3339(item.ModifiedTime),
			WebViewLink:  item.WebViewLink,
			Size:         size,
			Parents:      append([]string(nil), item.Parents...),
		})
	}
	return Page[DriveFile]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) Doc(ctx context.Context, documentID string) (DocDocument, error) {
	if strings.TrimSpace(documentID) == "" {
		return DocDocument{}, errors.New("document id is required")
	}
	params, _ := json.Marshal(map[string]string{"documentId": documentID})
	var raw rawDocDocument
	if err := c.runJSON(ctx, &raw, "docs", "documents", "get", "--params", string(params), "--format", "json"); err != nil {
		return DocDocument{}, err
	}
	blocks, attachments := docBlocks(raw.Body.Content, raw.InlineObjects)
	return DocDocument{
		ID:          fallback(raw.DocumentID, documentID),
		Title:       fallback(raw.Title, "(untitled document)"),
		Body:        docPlainText(blocks),
		Blocks:      blocks,
		Attachments: attachments,
	}, nil
}

func docBodyText(content []rawDocStructuralElement) string {
	blocks, _ := docBlocks(content, nil)
	return docPlainText(blocks)
}

type docBlockParser struct {
	inlineObjects map[string]rawDocInlineObject
	seenImages    map[string]bool
}

func docBlocks(content []rawDocStructuralElement, inlineObjects map[string]rawDocInlineObject) ([]DocBlock, []Attachment) {
	parser := docBlockParser{
		inlineObjects: inlineObjects,
		seenImages:    map[string]bool{},
	}
	return parser.blocks(content)
}

func (p *docBlockParser) blocks(content []rawDocStructuralElement) ([]DocBlock, []Attachment) {
	var blocks []DocBlock
	var attachments []Attachment
	for _, block := range content {
		if block.Paragraph != nil {
			paragraphBlocks, paragraphAttachments := p.paragraphBlocks(block.Paragraph)
			blocks = append(blocks, paragraphBlocks...)
			attachments = append(attachments, paragraphAttachments...)
		}
		if block.Table != nil {
			tableBlock, tableAttachments, ok := p.tableBlock(block.Table)
			if ok {
				blocks = append(blocks, tableBlock)
			}
			attachments = append(attachments, tableAttachments...)
		}
	}
	return blocks, attachments
}

func (p *docBlockParser) paragraphBlocks(paragraph *rawDocParagraph) ([]DocBlock, []Attachment) {
	if paragraph == nil {
		return nil, nil
	}
	kind, level := docParagraphKind(paragraph)
	listLevel := 0
	if paragraph.Bullet != nil {
		listLevel = max(0, paragraph.Bullet.NestingLevel)
	}
	var blocks []DocBlock
	var attachments []Attachment
	var inlines []DocInline

	flushText := func() {
		text := docInlinePlainText(inlines, true)
		if strings.TrimSpace(text) == "" {
			inlines = nil
			return
		}
		blocks = append(blocks, DocBlock{
			Kind:      kind,
			Text:      text,
			Inlines:   append([]DocInline(nil), inlines...),
			Level:     level,
			ListLevel: listLevel,
		})
		inlines = nil
	}

	for _, element := range paragraph.Elements {
		if element.TextRun != nil {
			inline := docInlineFromTextRun(element.TextRun)
			if inline.Text != "" {
				inlines = append(inlines, inline)
			}
			continue
		}
		if element.InlineObjectElement != nil {
			flushText()
			imageBlock, attachment, ok := p.imageBlock(element.InlineObjectElement.InlineObjectID)
			if ok {
				blocks = append(blocks, imageBlock)
				if attachment != nil {
					attachments = append(attachments, *attachment)
				}
			}
		}
	}
	flushText()
	return blocks, attachments
}

func docParagraphKind(paragraph *rawDocParagraph) (DocBlockKind, int) {
	if paragraph != nil && paragraph.Bullet != nil {
		return DocBlockListItem, max(0, paragraph.Bullet.NestingLevel)
	}
	named := ""
	if paragraph != nil {
		named = strings.ToUpper(strings.TrimSpace(paragraph.ParagraphStyle.NamedStyleType))
	}
	switch named {
	case "TITLE":
		return DocBlockTitle, 1
	case "SUBTITLE":
		return DocBlockSubtitle, 2
	}
	if strings.HasPrefix(named, "HEADING_") {
		level, err := strconv.Atoi(strings.TrimPrefix(named, "HEADING_"))
		if err != nil || level < 1 {
			level = 1
		}
		return DocBlockHeading, level
	}
	return DocBlockParagraph, 0
}

func docInlineFromTextRun(run *rawDocTextRun) DocInline {
	if run == nil {
		return DocInline{}
	}
	return DocInline{
		Text:          docCleanRunText(run.Content),
		Bold:          run.TextStyle.Bold,
		Italic:        run.TextStyle.Italic,
		Underline:     run.TextStyle.Underline,
		Strikethrough: run.TextStyle.Strikethrough,
		LinkURL:       strings.TrimSpace(run.TextStyle.Link.URL),
	}
}

func docCleanRunText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\ue907", "")
	return strings.TrimRight(text, "\n")
}

func (p *docBlockParser) imageBlock(inlineObjectID string) (DocBlock, *Attachment, bool) {
	inlineObjectID = strings.TrimSpace(inlineObjectID)
	if inlineObjectID == "" {
		return DocBlock{}, nil, false
	}
	object, ok := p.inlineObjects[inlineObjectID]
	if !ok {
		return DocBlock{Kind: DocBlockImage, Text: "image " + inlineObjectID}, nil, true
	}
	embedded := object.InlineObjectProperties.EmbeddedObject
	label := fallback(strings.TrimSpace(embedded.Title), strings.TrimSpace(embedded.Description))
	if label == "" {
		label = "image " + inlineObjectID
	}
	source := strings.TrimSpace(embedded.ImageProperties.ContentURI)
	if source == "" {
		source = strings.TrimSpace(embedded.ImageProperties.SourceURI)
	}
	block := DocBlock{Kind: DocBlockImage, Text: label}
	if source == "" {
		return block, nil, true
	}
	attachment := Attachment{
		ID:          "doc-image-" + inlineObjectID,
		Name:        label,
		ContentType: "image/png",
		URL:         source,
	}
	block.Attachment = &attachment
	key := attachment.PreviewSource()
	if key == "" || p.seenImages[key] {
		return block, nil, true
	}
	p.seenImages[key] = true
	return block, &attachment, true
}

func (p *docBlockParser) tableBlock(table *rawDocTable) (DocBlock, []Attachment, bool) {
	if table == nil {
		return DocBlock{}, nil, false
	}
	var rows [][]string
	var attachments []Attachment
	for _, row := range table.TableRows {
		cells := make([]string, 0, len(row.TableCells))
		for _, cell := range row.TableCells {
			cellBlocks, cellAttachments := p.blocks(cell.Content)
			cells = append(cells, strings.Join(strings.Fields(docPlainText(cellBlocks)), " "))
			attachments = append(attachments, cellAttachments...)
		}
		if len(cells) == 0 {
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return DocBlock{}, attachments, false
	}
	return DocBlock{Kind: DocBlockTable, Rows: rows, Text: docTablePlainText(rows)}, attachments, true
}

func docInlinePlainText(inlines []DocInline, includeLinks bool) string {
	var b strings.Builder
	for _, inline := range inlines {
		text := inline.Text
		if includeLinks && inline.LinkURL != "" && !strings.Contains(text, inline.LinkURL) {
			if strings.TrimSpace(text) == "" {
				text = inline.LinkURL
			} else {
				text += " (" + inline.LinkURL + ")"
			}
		}
		b.WriteString(text)
	}
	return b.String()
}

func docPlainText(blocks []DocBlock) string {
	var lines []string
	for _, block := range blocks {
		switch block.Kind {
		case DocBlockImage:
			if strings.TrimSpace(block.Text) != "" {
				lines = append(lines, "[image: "+strings.TrimSpace(block.Text)+"]")
			}
		case DocBlockTable:
			if table := docTablePlainText(block.Rows); table != "" {
				lines = append(lines, table)
			}
		default:
			text := strings.TrimSpace(block.Text)
			if text == "" {
				text = strings.TrimSpace(docInlinePlainText(block.Inlines, true))
			}
			if text != "" {
				if block.Kind == DocBlockListItem {
					text = strings.Repeat("  ", max(0, block.ListLevel)) + "- " + text
				}
				lines = append(lines, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func docTablePlainText(rows [][]string) string {
	var lines []string
	for _, row := range rows {
		line := strings.TrimSpace(strings.Join(row, " | "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

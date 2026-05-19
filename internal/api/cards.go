package api

import "encoding/json"

// rawCardEnvelope mirrors a single entry in the Chat REST API's
// `message.cardsV2` array: an outer { cardId, card } wrapper.
type rawCardEnvelope struct {
	CardID string  `json:"cardId"`
	Card   rawCard `json:"card"`
}

type rawCard struct {
	Header   *CardHeader  `json:"header,omitempty"`
	Sections []rawSection `json:"sections,omitempty"`
}

type rawSection struct {
	Header   string      `json:"header,omitempty"`
	Widgets  []rawWidget `json:"widgets,omitempty"`
	Collapse bool        `json:"collapsible,omitempty"`
}

// rawWidget keeps each widget kind as a json.RawMessage. We can't use a
// tagged union because the API discriminates by which field is present, not
// by a "type" field — so we decode the wrapper, then dispatch on the first
// non-null field.
type rawWidget struct {
	DecoratedText json.RawMessage `json:"decoratedText,omitempty"`
	TextParagraph json.RawMessage `json:"textParagraph,omitempty"`
	ButtonList    json.RawMessage `json:"buttonList,omitempty"`
	Image         json.RawMessage `json:"image,omitempty"`
	Divider       json.RawMessage `json:"divider,omitempty"`
	Columns       json.RawMessage `json:"columns,omitempty"`
	Grid          json.RawMessage `json:"grid,omitempty"`
	// Anything else (selectionInput, dateTimePicker, chipList, ...) gets
	// captured as an unknown so the renderer can mark it as dropped.
	Other map[string]json.RawMessage `json:"-"`
}

func decodeCards(raw json.RawMessage) []ChatCard {
	if len(raw) == 0 {
		return nil
	}
	var envelopes []rawCardEnvelope
	if err := json.Unmarshal(raw, &envelopes); err != nil {
		return nil
	}
	cards := make([]ChatCard, 0, len(envelopes))
	for _, env := range envelopes {
		card := ChatCard{ID: env.CardID, Header: env.Card.Header}
		for _, section := range env.Card.Sections {
			for _, w := range section.Widgets {
				widget := decodeWidget(w)
				widget.SectionHeader = section.Header
				card.Widgets = append(card.Widgets, widget)
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// decodeWidget inspects which discriminator field carries a value and
// produces the corresponding typed payload. Multiple non-empty fields would
// be malformed input; we honor the first one we see in schema order, which
// matches the Card v2 spec's "oneof" semantics.
func decodeWidget(w rawWidget) CardWidget {
	switch {
	case len(w.DecoratedText) > 0:
		var v decoratedTextRaw
		if err := json.Unmarshal(w.DecoratedText, &v); err == nil {
			return CardWidget{
				Kind: CardWidgetDecoratedText,
				DecoratedText: &DecoratedTextWidget{
					TopLabel:    v.TopLabel,
					Text:        v.Text,
					BottomLabel: v.BottomLabel,
					Icon:        v.Icon,
					StartIcon:   v.StartIcon,
					WrapText:    v.WrapText,
					URL:         openLinkURL(v.OnClick),
				},
			}
		}
	case len(w.TextParagraph) > 0:
		var v TextParagraphWidget
		if err := json.Unmarshal(w.TextParagraph, &v); err == nil {
			return CardWidget{Kind: CardWidgetTextParagraph, TextParagraph: &v}
		}
	case len(w.ButtonList) > 0:
		var v buttonListRaw
		if err := json.Unmarshal(w.ButtonList, &v); err == nil {
			buttons := make([]CardButton, 0, len(v.Buttons))
			for _, b := range v.Buttons {
				buttons = append(buttons, CardButton{
					Text:     b.Text,
					AltText:  b.AltText,
					URL:      openLinkURL(b.OnClick),
					Disabled: b.Disabled,
				})
			}
			return CardWidget{Kind: CardWidgetButtonList, ButtonList: &ButtonListWidget{Buttons: buttons}}
		}
	case len(w.Image) > 0:
		var v imageRaw
		if err := json.Unmarshal(w.Image, &v); err == nil {
			return CardWidget{Kind: CardWidgetImage, Image: &ImageWidget{URL: v.ImageURL, AltText: v.AltText}}
		}
	case len(w.Divider) > 0:
		return CardWidget{Kind: CardWidgetDivider}
	case len(w.Columns) > 0:
		var v columnsRaw
		if err := json.Unmarshal(w.Columns, &v); err == nil {
			cols := make([][]CardWidget, 0, len(v.ColumnItems))
			for _, col := range v.ColumnItems {
				inner := make([]CardWidget, 0, len(col.Widgets))
				for _, cw := range col.Widgets {
					inner = append(inner, decodeWidget(cw))
				}
				cols = append(cols, inner)
			}
			return CardWidget{Kind: CardWidgetColumns, Columns: &ColumnsWidget{Columns: cols}}
		}
	case len(w.Grid) > 0:
		var v gridRaw
		if err := json.Unmarshal(w.Grid, &v); err == nil {
			items := make([]GridItem, 0, len(v.Items))
			for _, it := range v.Items {
				items = append(items, GridItem{
					Title:    it.Title,
					Subtitle: it.Subtitle,
					ImageURL: imageURL(it.Image),
				})
			}
			return CardWidget{Kind: CardWidgetGrid, Grid: &GridWidget{Title: v.Title, Items: items}}
		}
	}
	for key, payload := range w.Other {
		if len(payload) > 0 {
			return CardWidget{Kind: CardWidgetUnknown, UnknownType: key}
		}
	}
	return CardWidget{Kind: CardWidgetUnknown}
}

// UnmarshalJSON gives rawWidget a custom decoder so unknown discriminator
// keys land in Other. Without this, json.Unmarshal would silently drop them
// and the renderer would never know the widget existed.
func (w *rawWidget) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	w.Other = map[string]json.RawMessage{}
	for key, value := range fields {
		switch key {
		case "decoratedText":
			w.DecoratedText = value
		case "textParagraph":
			w.TextParagraph = value
		case "buttonList":
			w.ButtonList = value
		case "image":
			w.Image = value
		case "divider":
			w.Divider = value
		case "columns":
			w.Columns = value
		case "grid":
			w.Grid = value
		case "horizontalAlignment":
			// styling-only, ignore
		default:
			w.Other[key] = value
		}
	}
	return nil
}

type decoratedTextRaw struct {
	TopLabel    string    `json:"topLabel,omitempty"`
	Text        string    `json:"text,omitempty"`
	BottomLabel string    `json:"bottomLabel,omitempty"`
	Icon        *CardIcon `json:"icon,omitempty"`
	StartIcon   *CardIcon `json:"startIcon,omitempty"`
	WrapText    bool      `json:"wrapText,omitempty"`
	OnClick     *onClick  `json:"onClick,omitempty"`
}

type buttonListRaw struct {
	Buttons []buttonRaw `json:"buttons,omitempty"`
}

type buttonRaw struct {
	Text     string   `json:"text,omitempty"`
	AltText  string   `json:"altText,omitempty"`
	OnClick  *onClick `json:"onClick,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

type imageRaw struct {
	ImageURL string `json:"imageUrl,omitempty"`
	AltText  string `json:"altText,omitempty"`
}

type columnsRaw struct {
	ColumnItems []columnItemRaw `json:"columnItems,omitempty"`
}

type columnItemRaw struct {
	Widgets []rawWidget `json:"widgets,omitempty"`
}

type gridRaw struct {
	Title string        `json:"title,omitempty"`
	Items []gridItemRaw `json:"items,omitempty"`
}

type gridItemRaw struct {
	Title    string         `json:"title,omitempty"`
	Subtitle string         `json:"subtitle,omitempty"`
	Image    *gridItemImage `json:"image,omitempty"`
}

type gridItemImage struct {
	ImageURL string `json:"imageUri,omitempty"`
}

type onClick struct {
	OpenLink *openLink `json:"openLink,omitempty"`
}

type openLink struct {
	URL string `json:"url,omitempty"`
}

func openLinkURL(o *onClick) string {
	if o == nil || o.OpenLink == nil {
		return ""
	}
	return o.OpenLink.URL
}

func imageURL(img *gridItemImage) string {
	if img == nil {
		return ""
	}
	return img.ImageURL
}

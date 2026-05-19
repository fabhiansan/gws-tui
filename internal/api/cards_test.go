package api

import "testing"

func TestDecodeCardsPreservesUnknownWidgets(t *testing.T) {
	raw := []byte(`[
		{
			"cardId": "id-1",
			"card": {
				"header": {"title": "T", "subtitle": "S"},
				"sections": [
					{"widgets": [
						{"decoratedText": {"topLabel": "Task", "text": "<b>X</b>", "icon": {"knownIcon": "TICKET"}}},
						{"selectionInput": {"name": "sel"}},
						{"buttonList": {"buttons": [{"text": "Go", "onClick": {"openLink": {"url": "https://x"}}}]}},
						{"divider": {}}
					]}
				]
			}
		}
	]`)
	cards := decodeCards(raw)
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.ID != "id-1" || c.Header == nil || c.Header.Title != "T" || c.Header.Subtitle != "S" {
		t.Fatalf("header lost: %+v", c.Header)
	}
	if len(c.Widgets) != 4 {
		t.Fatalf("expected 4 widgets, got %d", len(c.Widgets))
	}
	if c.Widgets[0].Kind != CardWidgetDecoratedText || c.Widgets[0].DecoratedText.Text != "<b>X</b>" {
		t.Errorf("decoratedText decoded wrong: %+v", c.Widgets[0])
	}
	if c.Widgets[0].DecoratedText.Icon == nil || c.Widgets[0].DecoratedText.Icon.KnownIcon != "TICKET" {
		t.Errorf("icon lost: %+v", c.Widgets[0].DecoratedText)
	}
	if c.Widgets[1].Kind != CardWidgetUnknown || c.Widgets[1].UnknownType != "selectionInput" {
		t.Errorf("unknown widget not tagged correctly: %+v", c.Widgets[1])
	}
	if c.Widgets[2].Kind != CardWidgetButtonList || c.Widgets[2].ButtonList.Buttons[0].URL != "https://x" {
		t.Errorf("button URL lost: %+v", c.Widgets[2])
	}
	if c.Widgets[3].Kind != CardWidgetDivider {
		t.Errorf("divider lost: %+v", c.Widgets[3])
	}
}

func TestDecodeCardsFlattensSections(t *testing.T) {
	raw := []byte(`[
		{
			"card": {
				"sections": [
					{"header": "First", "widgets": [{"textParagraph": {"text": "a"}}]},
					{"header": "Second", "widgets": [{"textParagraph": {"text": "b"}}]}
				]
			}
		}
	]`)
	cards := decodeCards(raw)
	if len(cards) != 1 || len(cards[0].Widgets) != 2 {
		t.Fatalf("expected 2 widgets across sections, got %+v", cards)
	}
	if cards[0].Widgets[0].SectionHeader != "First" || cards[0].Widgets[1].SectionHeader != "Second" {
		t.Errorf("section headers not preserved on widgets: %+v", cards[0].Widgets)
	}
}

func TestDecodeCardsEmptyAndInvalid(t *testing.T) {
	if got := decodeCards(nil); got != nil {
		t.Errorf("nil input should yield nil, got %+v", got)
	}
	if got := decodeCards([]byte(`"not an array"`)); got != nil {
		t.Errorf("malformed input should yield nil, got %+v", got)
	}
}

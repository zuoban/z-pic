package dropimport

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
)

func representation(format transfer.Format, text string) transfer.Representation {
	return transfer.Bytes(format, []byte(text))
}

func TestLinesWhitespaceAndComments(test *testing.T) {
	for _, scenario := range []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"blank and comment", "\r\n \t\n  # ignored\r\n", nil},
		{"mixed endings", " first\r\n\n # ignored\nsecond \t\n third\r\n", []string{"first", "second", "third"}},
		{"embedded carriage return", "first\rsecond\r\nthird\r", []string{"first\rsecond", "third"}},
		{"unicode whitespace", "\u2003first\u00a0\n\u2003# ignored\nsecond#fragment", []string{"first", "second#fragment"}},
		{"last line", "first\nlast", []string{"first", "last"}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			var actual []string
			for line := range lines(scenario.input) {
				actual = append(actual, line)
			}
			if !reflect.DeepEqual(actual, scenario.want) {
				test.Fatalf("lines = %#v; want %#v", actual, scenario.want)
			}
		})
	}
}

func TestLinesStopEarlyAndRestart(test *testing.T) {
	sequence := lines("# comment\r\nfirst\r\nsecond\nthird")
	for line := range sequence {
		if line != "first" {
			test.Fatalf("first line = %q", line)
		}
		break
	}
	var actual []string
	for line := range sequence {
		actual = append(actual, line)
	}
	if want := []string{"first", "second", "third"}; !reflect.DeepEqual(actual, want) {
		test.Fatalf("restarted lines = %#v; want %#v", actual, want)
	}
}

func TestParseFileListSchemeBoundaries(test *testing.T) {
	fallback := []byte("fallback image")
	for _, scenario := range []struct {
		name string
		text string
		path string
	}{
		{"native punctuation", "/tmp/a?b#c%oops.png", "/tmp/a?b#c%oops.png"},
		{"native colon", "/tmp/album:photo.png", "/tmp/album:photo.png"},
		{"mixed case file scheme", "FiLe://localhost/tmp/a%20b.png", "/tmp/a b.png"},
		{"short file URI", "file:/tmp/a.png", "/tmp/a.png"},
		{"escaped punctuation", "file:///tmp/a%3Fb%23c.png", "/tmp/a?b#c.png"},
		{"remote host", "file://remote/tmp/a.png", ""},
		{"host case", "file://LOCALHOST/tmp/a.png", ""},
		{"credentials", "file://user@localhost/tmp/a.png", ""},
		{"query", "file:///tmp/a.png?q=1", ""},
		{"fragment", "file:///tmp/a.png#fragment", ""},
		{"invalid escape", "file:///tmp/a%GG.png", ""},
		{"escaped null", "file:///tmp/a%00b.png", ""},
		{"native null", "/tmp/a\x00b.png", ""},
		{"relative path", "relative/a.png", ""},
		{"HTTP in file list", "https://example.test/a.png", ""},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			data := transfer.New(transfer.NewItem(
				representation(transfer.FileList, scenario.text),
				transfer.Bytes(transfer.PNG, fallback),
			))
			want := []Item{{Data: fallback}}
			if scenario.path != "" {
				want = []Item{{Path: scenario.path}}
			}
			items, err := Parse(data)
			if err != nil || !reflect.DeepEqual(items, want) {
				test.Fatalf("Parse = %#v, %v; want %#v", items, err, want)
			}
		})
	}
}

func TestParseHTMLKeepsProviderOrderAndFallbacks(test *testing.T) {
	fallback := []byte("fallback image")
	for _, scenario := range []struct {
		name      string
		markup    string
		want      []Item
		textReads int
	}{
		{"empty HTML", "", []Item{{Path: "/tmp/text.png"}}, 1},
		{"non-image HTML", "<p>description</p>", []Item{{Path: "/tmp/text.png"}}, 1},
		{"relative image", `<img src="../photo.png#fragment">`, []Item{{URL: "https://example.test/photo.png"}}, 0},
		{"unreadable image", `<img src="blob:https://example.test/image">`, []Item{{Data: fallback}}, 0},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			htmlReads, textReads := 0, 0
			data := transfer.New(transfer.NewItem(
				representation(transfer.URIList, "# comment\r\nblob:https://example.test/id\r\nhttps://example.test/album/page"),
				transfer.Lazy(transfer.HTML, func() ([]byte, error) {
					htmlReads++
					return []byte(scenario.markup), nil
				}),
				transfer.Lazy(transfer.Text, func() ([]byte, error) {
					textReads++
					return []byte("/tmp/text.png"), nil
				}),
				transfer.Bytes(transfer.PNG, fallback),
			))
			items, err := Parse(data)
			if err != nil || !reflect.DeepEqual(items, scenario.want) ||
				htmlReads != 1 || textReads != scenario.textReads {
				test.Fatalf("Parse = %#v, %v; HTML reads %d, text reads %d; want %#v, 1, %d",
					items, err, htmlReads, textReads, scenario.want, scenario.textReads)
			}
		})
	}
}

func TestParseHTMLImageFallbackSkipsUnusableText(t *testing.T) {
	textReads := 0
	fallback := []byte("image fallback")
	data := transfer.New(transfer.NewItem(
		representation(transfer.HTML, `<img src="blob:https://example.test/image">`),
		transfer.Lazy(transfer.Text, func() ([]byte, error) {
			textReads++
			return bytes.Repeat([]byte("x"), maxText+1), nil
		}),
		transfer.Bytes(transfer.PNG, fallback),
	))
	items, err := Parse(data)
	if err != nil || textReads != 0 || len(items) != 1 || !bytes.Equal(items[0].Data, fallback) {
		t.Fatalf("unusable text rejected image fallback: items=%v err=%v reads=%d", items, err, textReads)
	}
}

func TestParsePrecedence(t *testing.T) {
	tests := []struct {
		name string
		reps []transfer.Representation
		want []Item
	}{
		{"local alternatives", []transfer.Representation{
			representation(transfer.HTML, `<img src="https://example.test/preview">`),
			representation(transfer.URIList, "https://example.test/page"),
			representation(transfer.FileList, "file:///tmp/a%20b.png\r\nfile://localhost/tmp/c.jpg"),
		}, []Item{{Path: "/tmp/a b.png"}, {Path: "/tmp/c.jpg"}}},
		{"uri local precedence", []transfer.Representation{
			representation(transfer.URIList, "# comment\r\nhttps://example.test/page\r\nfile:///tmp/a.png"),
			representation(transfer.HTML, `<img src="https://example.test/preview">`),
		}, []Item{{Path: "/tmp/a.png"}}},
		{"browser anchor", []transfer.Representation{
			representation(transfer.URIList, "https://example.test/page"),
			representation(transfer.Text, "https://example.test/page"),
			representation(transfer.HTML, `<a href="/page"><img src="https://cdn.test/original?a=1&amp;b=2"></a>`),
			representation(transfer.PNG, "preview"),
		}, []Item{{URL: "https://cdn.test/original?a=1&b=2"}}},
		{"relative images", []transfer.Representation{
			representation(transfer.URIList, "https://example.test/articles/page"),
			representation(transfer.HTML, `<IMG SRC="../image"><img src="//cdn.test/b.png">`),
		}, []Item{{URL: "https://example.test/image"}, {URL: "https://cdn.test/b.png"}}},
		{"url list", []transfer.Representation{
			representation(transfer.URIList, "# comment\r\nhttps://example.test/a#one\r\nhttps://example.test/a#two\r\nhttp://example.test/b"),
			representation(transfer.Text, "https://other.test"),
		}, []Item{{URL: "https://example.test/a"}, {URL: "http://example.test/b"}}},
		{"plain urls", []transfer.Representation{
			representation(transfer.Text, "https://example.test/a\nhttp://example.test/b"),
		}, []Item{{URL: "https://example.test/a"}, {URL: "http://example.test/b"}}},
		{"plain file uri", []transfer.Representation{
			representation(transfer.Text, "file:///tmp/a.png"),
		}, []Item{{Path: "/tmp/a.png"}}},
		{"blob fallback", []transfer.Representation{
			representation(transfer.HTML, `<img src="blob:https://example.test/id">`),
			representation(transfer.URIList, "https://example.test/page"),
			representation(transfer.PNG, "png bytes"),
		}, []Item{{Data: []byte("png bytes")}}},
		{"blob uri fallback", []transfer.Representation{
			representation(transfer.URIList, "blob:https://example.test/id"),
			representation("image/jpeg", "jpeg bytes"),
		}, []Item{{Data: []byte("jpeg bytes")}}},
		{"png before jpeg", []transfer.Representation{
			representation("image/jpeg", "jpeg bytes"),
			representation(transfer.PNG, "png bytes"),
		}, []Item{{Data: []byte("png bytes")}}},
		{"browser webp", []transfer.Representation{
			representation(transfer.URIList, "https://example.test/page"),
			representation(transfer.HTML, `<img src="https://cdn.test/original.webp">`),
			representation(transfer.PNG, "preview"),
		}, []Item{{URL: "https://cdn.test/original.webp"}}},
		{"jpeg before webp", []transfer.Representation{
			representation("image/webp", "webp bytes"),
			representation("image/jpeg", "jpeg bytes"),
		}, []Item{{Data: []byte("jpeg bytes")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(transfer.New(transfer.NewItem(test.reps...)))
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Parse = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestParseMultipleItemsAndLazyAlternatives(t *testing.T) {
	data := transfer.New(
		transfer.NewItem(representation(transfer.FileList, "file:///tmp/a.png\nfile:///tmp/./a.png"),
			transfer.Lazy(transfer.PNG, func() ([]byte, error) { t.Fatal("read unnecessary preview"); return nil, nil })),
		transfer.NewItem(representation(transfer.URIList, "https://example.test/a")),
		transfer.NewItem(representation(transfer.Text, "https://example.test/a\nhttps://example.test/b")),
	)
	got, err := Parse(data)
	want := []Item{{Path: "/tmp/a.png"}, {URL: "https://example.test/a"}, {URL: "https://example.test/b"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %#v, %v", got, err)
	}
}

func TestParseMixedDropPreservesValidItems(t *testing.T) {
	firstFailure := errors.New("first provider failed")
	secondFailure := errors.New("second provider failed")
	data := transfer.New(
		transfer.NewItem(representation(transfer.FileList, "file:///tmp/a.png")),
		transfer.NewItem(representation(transfer.Text, "unsupported text")),
		transfer.NewItem(transfer.Lazy(transfer.HTML, func() ([]byte, error) { return nil, firstFailure })),
		transfer.NewItem(representation(transfer.URIList, "https://example.test/a")),
		transfer.NewItem(transfer.Lazy(transfer.PNG, func() ([]byte, error) { return nil, secondFailure })),
		transfer.NewItem(representation(transfer.FileList, "file:///tmp/./a.png")),
		transfer.NewItem(representation(transfer.Text, "https://example.test/b")),
	)
	got, err := Parse(data)
	want := []Item{{Path: "/tmp/a.png"}, {URL: "https://example.test/a"}, {URL: "https://example.test/b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %#v; want %#v", got, want)
	}
	if !errors.Is(err, firstFailure) || !errors.Is(err, secondFailure) || !strings.Contains(err.Error(), "无法导入") {
		t.Fatalf("missing joined drop errors: %v", err)
	}
}

func TestParseHardLimitsRejectWholeDrop(t *testing.T) {
	var urls strings.Builder
	for index := 0; index <= maxItems; index++ {
		fmt.Fprintf(&urls, "https://example.test/%d\n", index)
	}
	for _, text := range []string{
		strings.Repeat("a", maxText+1),
		urls.String(),
		strings.TrimSuffix(urls.String(), fmt.Sprintf("https://example.test/%d\n", maxItems)),
	} {
		data := transfer.New(
			transfer.NewItem(representation(transfer.FileList, "file:///tmp/valid.png")),
			transfer.NewItem(representation(transfer.Text, text),
				transfer.Lazy(transfer.PNG, func() ([]byte, error) {
					t.Error("read fallback after resource limit")
					return []byte("fallback"), nil
				})),
			transfer.NewItem(transfer.Lazy(transfer.Text, func() ([]byte, error) {
				t.Error("read later item after resource limit")
				return []byte("https://example.test/later"), nil
			})),
		)
		if got, err := Parse(data); got != nil || err == nil {
			t.Fatalf("hard limit returned %#v, %v", got, err)
		}
	}
}

func TestParseTransferBudgetStopsProviders(t *testing.T) {
	budget := 4
	source := transfer.NewItem(
		representation(transfer.HTML, "<img>"),
		transfer.Lazy(transfer.PNG, func() ([]byte, error) {
			t.Error("read fallback after total transfer budget exceeded")
			return []byte("image"), nil
		}),
	)
	got, err := parseItem(source, &budget)
	var limit resourceLimitError
	if got != nil || !errors.As(err, &limit) {
		t.Fatalf("budget exceeded returned %#v, %v", got, err)
	}
}

func TestParseLocalItemLimitStopsProviders(t *testing.T) {
	for _, format := range []transfer.Format{transfer.FileList, transfer.URIList, transfer.Text} {
		t.Run(string(format), func(t *testing.T) {
			value := strings.Repeat("file:///tmp/repeated.png\n", maxItems)
			items, err := Parse(transfer.New(transfer.NewItem(representation(format, value))))
			if err != nil || len(items) != 1 {
				t.Fatalf("at limit = %v, %v", items, err)
			}
			data := transfer.New(
				transfer.NewItem(
					representation(format, value+"file:///tmp/repeated.png"),
					transfer.Lazy(transfer.PNG, func() ([]byte, error) {
						t.Fatal("read fallback after local item limit")
						return nil, nil
					}),
				),
				transfer.NewItem(transfer.Lazy(transfer.Text, func() ([]byte, error) {
					t.Fatal("read later provider after local item limit")
					return nil, nil
				})),
			)
			items, err = Parse(data)
			var limit resourceLimitError
			if items != nil || !errors.As(err, &limit) {
				t.Fatalf("over limit = %v, %v", items, err)
			}
		})
	}
}

func TestParseWebPBytes(t *testing.T) {
	payload := imageBytes(t, "webp")
	data := transfer.New(transfer.NewItem(
		representation(transfer.HTML, `<img src="blob:https://example.test/id">`),
		representation(transfer.URIList, "https://example.test/page"),
		transfer.Bytes("image/webp", payload),
	))
	got, err := Parse(data)
	if err != nil || len(got) != 1 || !bytes.Equal(got[0].Data, payload) {
		t.Fatalf("Parse = %#v, %v", got, err)
	}
}

func TestParseDataURIs(t *testing.T) {
	for _, mime := range []string{"image/png", "image/jpeg", "image/webp"} {
		payload := []byte("test image bytes")
		if mime == "image/webp" {
			payload = imageBytes(t, "webp")
		}
		raw := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(payload)
		for _, rep := range []transfer.Representation{
			representation(transfer.Text, raw),
			representation(transfer.HTML, `<img src="`+raw+`">`),
		} {
			got, err := Parse(transfer.New(transfer.NewItem(rep)))
			if err != nil || len(got) != 1 || !bytes.Equal(got[0].Data, payload) {
				t.Fatalf("Parse = %#v, %v", got, err)
			}
		}
	}
}

func TestParseUnsupportedAndBounds(t *testing.T) {
	for _, text := range []string{"blob:https://example.test/id", "javascript:alert(1)", "hello", "file://remote/tmp/a", "https://user:pass@example.test/a", "data:image/gif;base64,YQ=="} {
		got, err := Parse(transfer.TextData(text))
		if err == nil || got != nil || !strings.Contains(err.Error(), "请") {
			t.Fatalf("%q: %#v, %v", text, got, err)
		}
	}
	if _, err := Parse(transfer.Data{}); err == nil {
		t.Fatal("empty drop accepted")
	}
	if _, err := Parse(transfer.TextData(strings.Repeat("a", maxText+1))); err == nil {
		t.Fatal("oversized text accepted")
	}
	items := make([]transfer.Item, maxItems+1)
	if _, err := Parse(transfer.New(items...)); err == nil {
		t.Fatal("too many native items accepted")
	}
	var list strings.Builder
	for index := 0; index <= maxItems; index++ {
		fmt.Fprintf(&list, "https://example.test/%d\n", index)
	}
	if _, err := Parse(transfer.TextData(list.String())); err == nil {
		t.Fatal("too many URLs accepted")
	}
	failure := errors.New("provider failed")
	if _, err := Parse(transfer.New(transfer.NewItem(transfer.Lazy(transfer.HTML, func() ([]byte, error) { return nil, failure })))); !errors.Is(err, failure) {
		t.Fatalf("missing provider error: %v", err)
	}
	got, err := Parse(transfer.New(transfer.NewItem(transfer.Lazy(transfer.HTML, func() ([]byte, error) { return nil, failure }), representation(transfer.PNG, "fallback"))))
	if err != nil || len(got) != 1 {
		t.Fatalf("provider fallback: %#v, %v", got, err)
	}
}

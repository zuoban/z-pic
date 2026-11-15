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

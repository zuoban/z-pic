// Package dropimport turns native drop representations into local image inputs.
package dropimport

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/transfer"
	"golang.org/x/net/html"
)

const (
	maxItems    = 2000
	maxText     = 1 << 20
	maxImage    = 64 << 20
	maxTransfer = 128 << 20
)

// Item describes one image. Parse sets exactly one of Path, URL, and Data.
type Item struct {
	Path string
	URL  string
	Data []byte
}

// Parse preserves item order and chooses one representation per native item.
// Lists within a representation may yield multiple images. Unsupported items
// and failed providers without a usable alternative produce joined errors
// alongside valid items. Resource limits stop parsing and reject the whole drop.
func Parse(data transfer.Data) ([]Item, error) {
	native := data.Items()
	if len(native) > maxItems {
		return nil, itemLimit()
	}
	var result []Item
	var failures []error
	seen := make(map[string]bool)
	budget := maxTransfer
	remainingItems := maxItems
	for _, source := range native {
		items, err := parseItem(source, &budget)
		if err != nil {
			failures = append(failures, err)
			var limit resourceLimitError
			if errors.As(err, &limit) {
				return nil, errors.Join(failures...)
			}
			continue
		}
		// Bound work across the entire transfer, including repeated references
		// split over several native items. Deduplication is a separate concern.
		if len(items) > remainingItems {
			return nil, errors.Join(append(failures, itemLimit())...)
		}
		remainingItems -= len(items)
		for _, item := range items {
			key := ""
			if item.Path != "" {
				key = "file:" + filepath.Clean(item.Path)
			} else if item.URL != "" {
				key = item.URL
			}
			if key != "" && seen[key] {
				continue
			}
			if key != "" {
				seen[key] = true
			}
			result = append(result, item)
		}
	}
	if len(result) == 0 && len(failures) == 0 {
		return nil, unsupported()
	}
	return result, errors.Join(failures...)
}

type resourceLimitError string

func (err resourceLimitError) Error() string {
	return string(err)
}

func itemLimit() error {
	return resourceLimitError(fmt.Sprintf("一次最多导入 %d 张图片", maxItems))
}

func parseItem(source transfer.Item, budget *int) (items []Item, err error) {
	var readErrors []error
	var resourceErr error
	defer func() {
		if resourceErr != nil {
			items, err = nil, errors.Join(append(readErrors, resourceErr)...)
		}
	}()
	read := func(format transfer.Format, limit int) []byte {
		if resourceErr != nil {
			return nil
		}
		value, err := source.Read(format)
		if errors.Is(err, transfer.ErrFormat) {
			return nil
		}
		if err != nil {
			readErrors = append(readErrors, err)
			return nil
		}
		if len(value) > limit || len(value) > *budget {
			resourceErr = resourceLimitError("拖放数据过大：文本最多 1 MiB，图片最多 64 MiB，总计最多 128 MiB")
			return nil
		}
		*budget -= len(value)
		return value
	}
	files := string(read(transfer.FileList, maxText))
	if items, err := localItems(files); err != nil || len(items) > 0 {
		return items, err
	}
	uris := string(read(transfer.URIList, maxText))
	if items, err := localItems(uris); err != nil || len(items) > 0 {
		return items, err
	}
	markup := read(transfer.HTML, maxText)
	hadImage := false
	if len(markup) > 0 {
		var base *url.URL
		for line := range lines(uris) {
			if parsed, err := httpURL(line); err == nil {
				base = parsed
				break
			}
		}
		var images []Item
		tokenizer := html.NewTokenizer(bytes.NewReader(markup))
		for {
			tokenType := tokenizer.Next()
			if tokenType == html.ErrorToken {
				if err := tokenizer.Err(); err != io.EOF {
					readErrors = append(readErrors, err)
				}
				break
			}
			if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
				continue
			}
			token := tokenizer.Token()
			if token.Data != "img" {
				continue
			}
			hadImage = true
			for _, attr := range token.Attr {
				if attr.Key != "src" {
					continue
				}
				raw := strings.TrimSpace(attr.Val)
				if parsed, err := url.Parse(raw); err == nil && base != nil && raw != "" {
					raw = base.ResolveReference(parsed).String()
				}
				if item, err := reference(raw); err == nil {
					images = append(images, item)
				}
				break
			}
			if len(images) > maxItems {
				return nil, itemLimit()
			}
		}
		if len(images) > 0 {
			return images, nil
		}
	}
	if !hadImage {
		text := string(read(transfer.Text, maxText))
		if items, err := localItems(text); err != nil || len(items) > 0 {
			return items, err
		}
		for _, representation := range []string{uris, text} {
			var items []Item
			for line := range lines(representation) {
				if item, err := reference(line); err == nil {
					items = append(items, item)
				}
				if len(items) > maxItems {
					return nil, itemLimit()
				}
			}
			if len(items) > 0 {
				return items, nil
			}
		}
	}
	for _, format := range []transfer.Format{transfer.PNG, "image/jpeg", "image/webp"} {
		if payload := read(format, maxImage); len(payload) > 0 {
			return []Item{{Data: payload}}, nil
		}
	}
	return nil, errors.Join(append([]error{unsupported()}, readErrors...)...)
}

func unsupported() error {
	return fmt.Errorf("无法导入此拖放内容：请拖入本地 JPEG/PNG/WebP、图片的 HTTP(S) 直链或图片数据；blob 图片请先保存到本地再拖入")
}

func lines(text string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for line := range strings.SplitSeq(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				if !yield(line) {
					return
				}
			}
		}
	}
}

func localItems(text string) ([]Item, error) {
	var result []Item
	for line := range lines(text) {
		path := line
		if scheme, _, ok := strings.Cut(line, ":"); ok && strings.EqualFold(scheme, "file") {
			parsed, err := url.Parse(line)
			if err != nil {
				continue
			}
			if parsed.User != nil || (parsed.Host != "" && parsed.Host != "localhost") || parsed.RawQuery != "" || parsed.Fragment != "" {
				continue
			}
			path = filepath.FromSlash(parsed.Path)
		}
		if filepath.IsAbs(path) && !strings.ContainsRune(path, 0) {
			if len(result) == maxItems {
				return nil, itemLimit()
			}
			result = append(result, Item{Path: path})
		}
	}
	return result, nil
}

func httpURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, fmt.Errorf("仅支持不含登录凭据的 HTTP(S) 图片直链")
	}
	parsed.Fragment = ""
	return parsed, nil
}

func reference(raw string) (Item, error) {
	if strings.HasPrefix(raw, "data:") {
		header, encoded, ok := strings.Cut(raw, ",")
		if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64" && header != "data:image/webp;base64") {
			return Item{}, unsupported()
		}
		payload, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(payload) == 0 || len(payload) > maxImage {
			return Item{}, unsupported()
		}
		return Item{Data: payload}, nil
	}
	parsed, err := httpURL(raw)
	if err != nil {
		return Item{}, err
	}
	return Item{URL: parsed.String()}, nil
}

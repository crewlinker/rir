package main

import (
	"bytes"
	"context"
	"html/template"
	"os"
	"testing"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func TestRenderScreenshot1(t *testing.T) {
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	v, err := template.ParseFS(tmpls, "*.gotmpl")
	if err != nil {
		t.Fatal(err)
	}

	var res bytes.Buffer
	if err = v.ExecuteTemplate(&res, "profile.gotmpl", struct{}{}); err != nil {
		t.Fatal(err)
	}

	var buf []byte
	if err := chromedp.Run(ctx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			ft, _ := page.GetFrameTree().Do(ctx)
			return page.SetDocumentContent(ft.Frame.ID, res.String()).Do(ctx)
		}),
		chromedp.FullScreenshot(&buf, 90),
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile("profile.screenshot.jpeg", buf, 0664); err != nil {
		t.Fatal(err)
	}
}

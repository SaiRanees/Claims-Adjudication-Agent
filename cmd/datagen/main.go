// Command datagen renders the synthetic claim packets and their truth from
// the scenario specs (week 3, sessions 3 and 4):
//
//	go run ./cmd/datagen render [-specs dir] [-out dir] [-only id] [-check]
//
// render writes each packet's PDFs and truth.json into <out>/<scenario>/.
// With -check it reads every page back with PDFium and runs the
// self-check. Rendered packets aren't committed; the specs are.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
)

// Exit statuses.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

const usage = `Usage:
  datagen render [-specs dir] [-out dir] [-only id] [-check]
      Render each scenario's PDFs and truth.json into <out>/<scenario>/
`

// The default places, relative to the repository's root.
const (
	defaultSpecs = "evals/extraction/scenarios"
	defaultOut   = "out/packets"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	var err error
	switch args[0] {
	case "render":
		err = render(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	if errors.Is(err, flag.ErrHelp) || errors.Is(err, errUsage) {
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(stderr, "datagen:", err)
		return exitFail
	}
	return exitOK
}

var errUsage = errors.New("usage")

func render(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specsDir := fs.String("specs", defaultSpecs, "directory of scenario specs")
	out := fs.String("out", defaultOut, "directory to write packets into")
	only := fs.String("only", "", "render only this scenario")
	check := fs.Bool("check", false, "read every page back with PDFium and run the self-check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	specs, err := datagen.LoadSpecs(os.DirFS(*specsDir))
	if err != nil {
		return err
	}
	var pdfium *documents.PDFium
	if *check {
		if pdfium, err = documents.NewPDFium(documents.DefaultPDFiumConfig); err != nil {
			return err
		}
		defer func() { _ = pdfium.Close() }()
	}
	rendered := 0
	for _, s := range specs {
		if *only != "" && s.ID != *only {
			continue
		}
		p, err := datagen.Render(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		if pdfium != nil {
			if err := datagen.SelfCheck(ctx, pdfium, p); err != nil {
				return fmt.Errorf("%s: self-check: %w", s.ID, err)
			}
		}
		if err := datagen.WritePacket(*out, p); err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		rendered++
	}
	if *only != "" && rendered == 0 {
		return fmt.Errorf("no scenario %q in %s", *only, *specsDir)
	}
	checked := ""
	if *check {
		checked = ", each self-checked"
	}
	fmt.Fprintf(stdout, "rendered %d packets into %s%s\n", rendered, *out, checked)
	return nil
}

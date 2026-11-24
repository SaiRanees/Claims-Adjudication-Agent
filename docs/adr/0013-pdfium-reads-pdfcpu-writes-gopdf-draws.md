# 0013. Read PDFs with PDFium in WebAssembly, write them with pdfcpu, and draw them with gopdf

- Status: Proposed
- Date: 2026-11-24

## Context

M2 handles PDFs in six ways: it checks an upload that may be hostile, splits it into pages and page ranges, reads each page's text with positions, renders pages, turns a photo into a PDF, and draws the synthetic packets the evals run on.

- The worker image is a static binary on distroless, built with `CGO_ENABLED=0`, and development is native Windows with nothing to install beyond Go and Docker Desktop ([ADR 0007](0007-go-task-runner.md)). A library that needs CGO or a system package, such as Poppler or a PDFium shared library, fits neither.
- Uploads are untrusted. A parser bug reached by a crafted file must not reach the worker's memory, files or network.
- Model calls are recorded and replayed by request hash ([ADR 0014](0014-record-and-replay-model-calls.md)), so whatever identifies a page range must be the same on every run and every OS.

Checked in each library's source on 24 November 2026:

- pdfcpu v0.16.4 stamps every file it writes. Its `fileID` hashes `time.Now()` into the trailer's `/ID` on each write, with no option to turn that off, and a new info dictionary gets the current time as `CreationDate` and `ModDate` even with `PreserveInfoDict`. The same page split from the same file twice comes out with different bytes.
- pdfcpu opens a PDF whose user password is empty without being asked; only a non-empty user password returns `ErrWrongPassword`. It doesn't read EXIF orientation when it turns an image into a PDF.
- go-pdfium v1.21.5 runs PDFium under wazero in its WebAssembly mode, with no CGO. Left at its defaults, the pool mounts the host's root directory into the module (on Windows, the working directory's drive), draws randomness from `crypto/rand`, and sets no memory limit. Its `Config` takes an `FSConfig`, a `RandomSource` and a wazero `RuntimeConfig`, and wazero's `RuntimeConfig` has `WithMemoryLimitPages`, `WithCloseOnContextDone` and `WithCompilationCache`. go-pdfium reads a page's text with positions, renders pages, and reports a document's security handler revision.
- gopdf v0.38.6 writes a trailer `/ID` only when protection is on, writes `CreationDate` only when one is set, reads the clock only to seed encryption keys, keeps its character-to-glyph map in order, and writes a ToUnicode map, so text it draws can be read back.
- Noto Sans has the rupee sign (glyph `rupeeIndian`, U+20B9), which PDF's built-in fonts lack, under the SIL Open Font License 1.1.
- Licences: go-pdfium MIT, PDFium Apache-2.0, wazero Apache-2.0, pdfcpu Apache-2.0, gopdf MIT.

## Decision

Three libraries, each with one job.

**go-pdfium, in WebAssembly mode, reads.** It is the first code to parse an uploaded PDF, and nothing else parses one until it has accepted it.

- The pool gets an empty `FSConfig`, so the module sees no host files, and documents load from bytes. It gets a fixed `RandomSource`, and a `RuntimeConfig` with a memory limit, `WithCloseOnContextDone(true)` and the exception-handling feature the bundled module needs. Every call runs under a deadline, the pool's size is capped, and the compiled module is cached.
- It decides `encrypted` (any security handler, whatever the password), `unreadable` (the file won't load) and the page count (at most 500).
- It reads each page's text with positions, and renders pages.

**pdfcpu writes.** It splits a file into pages and page ranges, merges ranges, and turns a photo into a one-page PDF.

- It only ever sees a PDF that PDFium has accepted, or a photo whose header has been checked. It parses outside the sandbox, so every call runs under a deadline and recovers a panic into `unreadable`.
- Its output carries the current time, so no identity rests on its bytes. A derived file (a page, a range, a photo's PDF) is identified by the uploaded file's SHA-256, its page numbers and a derivation version that is bumped by hand (ADR 0014). Repeated pages are found by hashing PDFium's rendering of the page, never the split file.
- A photo's orientation is read from its EXIF tag in our own code and applied as the page's `/Rotate`, so the image bytes stay as uploaded.

**gopdf draws** the generator's packets, with static Noto Sans Regular and Bold TTFs embedded through `go:embed` and committed with the licence text.

- No protection and no `CreationDate`, and compression off where gopdf allows it, so the bytes depend on the scenario spec and the generator alone.
- A test renders every template twice, in separate processes, and compares the bytes. Another pins one packet's SHA-256 on the Linux and Windows CI jobs.

## Consequences

- No CGO and no system install: the same code runs on Windows, in CI and in the distroless worker.
- A crafted PDF meets PDFium inside wazero, with no files, a memory cap and a deadline. pdfcpu stays a native parser, limited to files PDFium accepted.
- PDFium in WebAssembly is slower than native PDFium. Session 2 of week 3 measures a file check and a page's text read on synthetic packets, and sets the pool's size and the deadlines from that.
- There are two PDF parsers to keep current, and the worker binary carries PDFium's WebAssembly build.
- Split pages and photo PDFs differ from run to run in their `/ID` and dates. A test that pins a derived file compares it with those removed, so a pdfcpu upgrade that changes a page's content still fails it.
- PDFium can also import pages and save. If a test shows its output is byte-stable under a fixed random source, it could take over splitting from pdfcpu; that would be a new ADR.

## Alternatives considered

- **Poppler (pdftotext, pdftoppm),** which architecture.md named first. It needs a system install on Windows and in the image, or CGO, and parses hostile files natively.
- **go-pdfium through CGO,** single- or multi-threaded. It is faster, but it needs a PDFium shared library at build and run time, and CGO, which the image and ADR 0007 rule out.
- **pdfcpu alone.** It can't read text with positions or render a page.
- **Patching pdfcpu's `/ID` and dates after each write,** to make its output stable. It works until pdfcpu changes how it writes them, and identifying derived files by their source does the same job without touching bytes.
- **gofpdf for the generator.** Both `jung-kurt/gofpdf` and its fork `go-pdf/fpdf` are archived; gopdf is maintained, and its byte stability can be tested.

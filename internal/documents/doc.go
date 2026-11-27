// Package documents handles claim documents: short-lived upload URLs, checking
// each stored object against its declared size and SHA-256, page counts, and
// preparing an accepted file's pages (split, text with positions, a rendering
// fingerprint, and repeats).
package documents

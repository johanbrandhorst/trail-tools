package html

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/johanbrandhorst/trail-tools/web"
)

// bundle is the frontend's bundle embedded in the server.
var bundle = func() fs.FS {
	fsys, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	return fsys
}()

// bundleHashes returns the hashes of the bundle's files' contents, by path,
// e.g. "index.js". They're computed once, the first time they're needed,
// which takes a few milliseconds.
var bundleHashes = sync.OnceValue(func() map[string]string {
	hashes := map[string]string{}
	_ = fs.WalkDir(bundle, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := bundle.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		// Streamed, as reading the 6 MB source map would copy it.
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		hashes[p] = base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:12])
		return nil
	})
	return hashes
})

// versionParam is the query parameter of a bundle file's URL with the hash of
// its contents, e.g. "/index.js?v=<hash>". A URL with the hash always
// serves the same contents, so browsers can keep it.
const versionParam = "v"

// bundleURL returns the URL of a file of the bundle, with the hash of its
// contents.
func bundleURL(p string) string {
	if hash, ok := bundleHashes()[p]; ok {
		return "/" + p + "?" + versionParam + "=" + hash
	}
	return "/" + p
}

// ServeBundle serves the files of the embedded bundle. The files have no
// modification times, so they're tagged with the hashes of their contents:
// browsers check that theirs is current, and get it again only when it
// changed. Requests for a file with its hash, as index.html makes for the
// script and styles, can be cached for good, as the next bundle has another
// hash.
func ServeBundle() http.Handler {
	files := http.FileServerFS(bundle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash, ok := bundleHashes()[strings.TrimPrefix(path.Clean(r.URL.Path), "/")]
		if ok {
			// FileServer answers conditional requests with the ETag.
			w.Header().Set("ETag", `"`+hash+`"`)
			if r.URL.Query().Get(versionParam) == hash {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
		}
		files.ServeHTTP(w, r)
	})
}

package quiz

import (
	"fmt"
	"net/http"
	"strings"

	"rsc.io/qr"
)

// qrQuietZone is the blank border around a QR code, in modules. The
// standard asks for 4, which scanners rely on to find the code.
const qrQuietZone = 4

// qrSVG draws text as a QR code in SVG, dark on light so every scanner
// reads it. Each row's runs of dark modules are drawn as one path, which
// keeps the file small.
func qrSVG(text string) ([]byte, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, fmt.Errorf("failed to encode QR code: %w", err)
	}
	size := code.Size + 2*qrQuietZone
	var path strings.Builder
	for y := range code.Size {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			start := x
			for x < code.Size && code.Black(x, y) {
				x++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", start+qrQuietZone, y+qrQuietZone, x-start, x-start)
		}
	}
	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
		`<rect width="%d" height="%d" fill="#fffaf0"/><path fill="#2b2014" d="%s"/></svg>`,
		size, size, size, size, path.String()), nil
}

// qrHandler serves a QR code of the quiz's link, for guests to scan from the
// quiz master's screen.
func (s *Server) qrHandler() (http.Handler, error) {
	svg, err := qrSVG(s.publicURL)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(svg)
	}), nil
}

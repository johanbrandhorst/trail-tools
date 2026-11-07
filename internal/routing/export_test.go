package routing

import "github.com/johanbrandhorst/trail-tools/internal/geo"

// ReadElements reads OpenStreetMap data from nodes, ways and relations in the
// order of a PBF extract, as ReadPBF reads an extract.
func ReadElements(bounds geo.Bounds, elements []any) (*OSM, error) {
	return readOSM(bounds, func(handle func(any)) error {
		for _, e := range elements {
			handle(e)
		}
		return nil
	})
}

// SplitPath splits a path into pieces, as preferred paths are.
var SplitPath = splitPath

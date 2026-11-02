package garmin

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// exercisesCSV is Garmin's catalog of strength exercises, written by
// exercises/generate.mjs from Garmin's FIT SDK.
//
//go:embed exercises/exercises.csv
var exercisesCSV []byte

// Exercise is an exercise of Garmin's catalog, which watches show by name
// and count reps of. The Training API only takes exercises of the catalog.
type Exercise struct {
	// The category and name, as the Training API takes them, like "SQUAT"
	// and "GOBLET_SQUAT".
	Category, Name string
	// The numbers of the category and name in FIT files.
	FITCategory, FITName uint16
}

// String returns the exercise as "CATEGORY/NAME", as the API takes it.
func (e Exercise) String() string {
	return e.Category + "/" + e.Name
}

var (
	loadExercises sync.Once
	exercises     []Exercise
	// The exercises by "CATEGORY/NAME".
	exercisesByID map[string]Exercise
	// The indexes of exercises by name, which some categories share.
	exercisesByName map[string][]int
)

func catalog() []Exercise {
	loadExercises.Do(func() {
		rows, err := csv.NewReader(bytes.NewReader(exercisesCSV)).ReadAll()
		if err != nil {
			panic(fmt.Sprintf("invalid exercise catalog: %v", err))
		}
		exercisesByID = map[string]Exercise{}
		exercisesByName = map[string][]int{}
		for _, row := range rows[1:] {
			category, err := strconv.ParseUint(row[2], 10, 16)
			if err != nil {
				panic(fmt.Sprintf("invalid exercise catalog row %v: %v", row, err))
			}
			name, err := strconv.ParseUint(row[3], 10, 16)
			if err != nil {
				panic(fmt.Sprintf("invalid exercise catalog row %v: %v", row, err))
			}
			e := Exercise{Category: row[0], Name: row[1], FITCategory: uint16(category), FITName: uint16(name)}
			exercisesByID[e.String()] = e
			exercisesByName[e.Name] = append(exercisesByName[e.Name], len(exercises))
			exercises = append(exercises, e)
		}
	})
	return exercises
}

// LookupExercise returns the catalog's exercise of an ID like
// "SQUAT/GOBLET_SQUAT", ignoring case.
func LookupExercise(id string) (Exercise, bool) {
	catalog()
	e, ok := exercisesByID[strings.ToUpper(strings.TrimSpace(id))]
	return e, ok
}

// MatchExercise returns the catalog's exercise named like name, like
// "Goblet squat" or "Goblet squats" for "SQUAT/GOBLET_SQUAT", if exactly
// one is.
func MatchExercise(name string) (Exercise, bool) {
	all := catalog()
	key := catalogName(name)
	for _, candidate := range []string{key, strings.TrimSuffix(key, "S"), strings.TrimSuffix(key, "ES")} {
		if indexes := exercisesByName[candidate]; len(indexes) == 1 {
			return all[indexes[0]], true
		}
	}
	return Exercise{}, false
}

// SearchExercises returns up to limit exercises of the catalog whose
// category and name contain every word of query, the shortest names first.
func SearchExercises(query string, limit int) []Exercise {
	words := strings.Fields(catalogName(query))
	if len(words) == 1 {
		words = strings.Split(words[0], "_")
	}
	var found []Exercise
	for _, e := range catalog() {
		id := e.String()
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(id, w) }) {
			found = append(found, e)
		}
	}
	slices.SortStableFunc(found, func(a, b Exercise) int { return len(a.Name) - len(b.Name) })
	return found[:min(len(found), limit)]
}

// catalogName returns a name in the catalog's style: upper case words
// joined by underscores.
func catalogName(name string) string {
	fields := strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
	return strings.Join(fields, "_")
}

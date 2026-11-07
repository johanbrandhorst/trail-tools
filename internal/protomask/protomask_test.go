package protomask_test

import (
	"slices"
	"testing"

	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestApply(t *testing.T) {
	t.Parallel()
	newDst := func() *racesv1.Race {
		return racesv1.Race_builder{Id: "1", Name: "Old", Location: "Here", Notes: "Notes"}.Build()
	}
	src := racesv1.Race_builder{Id: "2", Name: "New", Location: ""}.Build()

	dst := newDst()
	if err := protomask.Apply(dst, src, &fieldmaskpb.FieldMask{Paths: []string{"name", "location"}}, "id"); err != nil {
		t.Fatal(err)
	}
	if dst.GetId() != "1" || dst.GetName() != "New" || dst.GetLocation() != "" || dst.GetNotes() != "Notes" {
		t.Errorf("unexpected result %v", dst)
	}

	dst = newDst()
	if err := protomask.Apply(dst, src, &fieldmaskpb.FieldMask{Paths: []string{"websiteUrl"}}); err != nil {
		t.Errorf("JSON name path: %v", err)
	}

	dst = newDst()
	if err := protomask.Apply(dst, src, nil, "id"); err != nil {
		t.Fatal(err)
	}
	if dst.GetId() != "1" || dst.GetName() != "New" || dst.GetNotes() != "" {
		t.Errorf("unexpected result for empty mask %v", dst)
	}

	for _, path := range []string{"nope", "id"} {
		if err := protomask.Apply(newDst(), src, &fieldmaskpb.FieldMask{Paths: []string{path}}, "id"); err == nil {
			t.Errorf("expected error for path %q", path)
		}
	}
}

func TestFields(t *testing.T) {
	t.Parallel()
	md := (&racesv1.Race{}).ProtoReflect().Descriptor()
	names := func(mask *fieldmaskpb.FieldMask) ([]string, error) {
		fields, err := protomask.Fields(md, mask, "id")
		var result []string
		for _, fd := range fields {
			result = append(result, string(fd.Name()))
		}
		return result, err
	}

	got, err := names(&fieldmaskpb.FieldMask{Paths: []string{"notes", "websiteUrl", "notes"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"notes", "website_url"}; !slices.Equal(got, want) {
		t.Errorf("got fields %v, want %v", got, want)
	}

	got, err = names(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != md.Fields().Len()-1 || slices.Contains(got, "id") {
		t.Errorf("unexpected fields for empty mask %v", got)
	}

	for _, path := range []string{"nope", "id"} {
		if _, err := names(&fieldmaskpb.FieldMask{Paths: []string{path}}); err == nil {
			t.Errorf("expected error for path %q", path)
		}
	}
}

func TestApplyNested(t *testing.T) {
	t.Parallel()
	newDst := func() *plansv1.Workout {
		return plansv1.Workout_builder{Id: "1", Title: "Long run", Run: plansv1.RunWorkout_builder{
			Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE, TargetDistanceMeters: 20000,
			Blocks:  []*plansv1.RunBlock{plansv1.RunBlock_builder{Repeats: 6}.Build()},
			Fueling: plansv1.RunFueling_builder{CarbsGramsPerHour: 60, FluidMlPerHour: 500}.Build(),
		}.Build()}.Build()
	}
	// The request carries only the field to change.
	src := plansv1.Workout_builder{Run: plansv1.RunWorkout_builder{
		LactateTest: true, Fueling: plansv1.RunFueling_builder{CarbsGramsPerHour: 80}.Build(),
	}.Build()}.Build()

	dst := newDst()
	if err := protomask.Apply(dst, src, &fieldmaskpb.FieldMask{Paths: []string{"run.lactateTest", "run.fueling.carbs_grams_per_hour"}}, "id"); err != nil {
		t.Fatal(err)
	}
	run := dst.GetRun()
	if !run.GetLactateTest() || run.GetFueling().GetCarbsGramsPerHour() != 80 ||
		run.GetFueling().GetFluidMlPerHour() != 500 || len(run.GetBlocks()) != 1 || run.GetTargetDistanceMeters() != 20000 || dst.GetTitle() != "Long run" {
		t.Errorf("got %v, want only the lactate test flag and the carbs changed", dst)
	}

	// A field missing from the request is cleared.
	dst = newDst()
	if err := protomask.Apply(dst, &plansv1.Workout{}, &fieldmaskpb.FieldMask{Paths: []string{"run.fueling"}}); err != nil {
		t.Fatal(err)
	}
	if dst.GetRun().HasFueling() || len(dst.GetRun().GetBlocks()) != 1 {
		t.Errorf("got %v, want only the fueling cleared", dst)
	}

	for _, path := range []string{
		// Not a field, or not a message.
		"run.nope", "title.length", "run.blocks.repeats",
		// A kind of workout dst isn't: it's switched by replacing the
		// whole field.
		"strength.target_duration_seconds",
		// Within a field that's never copied.
		"id.value",
	} {
		if err := protomask.Apply(newDst(), src, &fieldmaskpb.FieldMask{Paths: []string{path}}, "id"); err == nil {
			t.Errorf("expected an error for path %q", path)
		}
	}
}

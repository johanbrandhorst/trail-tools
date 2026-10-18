package services_test

import (
	"bytes"
	"context"
	"image/jpeg"
	"testing"

	"connectrpc.com/connect/v2"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
)

func TestGetTripPhoto(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	created, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{ActivityId: e.createActivity()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	upload, err := e.trips.UploadTripPhoto(ctx, tripsv1.UploadTripPhotoRequest_builder{
		ReportId: created.GetReport().GetId(),
		Data:     testJPEG(t, 3000, 2000),
		Caption:  "Summit",
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	photoID := upload.GetPhoto().GetId()

	// The image of each size, as a JPEG, with the photo. The thumbnail is
	// the default.
	for size, wantWidth := range map[tripsv1.PhotoSize]int{
		tripsv1.PhotoSize_PHOTO_SIZE_UNSPECIFIED: 480,
		tripsv1.PhotoSize_PHOTO_SIZE_THUMBNAIL:   480,
		tripsv1.PhotoSize_PHOTO_SIZE_LARGE:       2048,
	} {
		resp, err := e.trips.GetTripPhoto(ctx, tripsv1.GetTripPhotoRequest_builder{Id: photoID, Size: size}.Build())
		if err != nil {
			t.Fatalf("GetTripPhoto %v: %v", size, err)
		}
		if resp.GetPhoto().GetCaption() != "Summit" || resp.GetImage().GetMimeType() != "image/jpeg" {
			t.Errorf("got photo %v and image type %q", resp.GetPhoto(), resp.GetImage().GetMimeType())
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(resp.GetImage().GetData()))
		if err != nil || cfg.Width != wantWidth {
			t.Errorf("got a %v image %d wide (%v), want %d", size, cfg.Width, err, wantWidth)
		}
	}

	// Other users' photos aren't found, even of drafts they can't see.
	_, err = e.otherTrips.GetTripPhoto(ctx, tripsv1.GetTripPhotoRequest_builder{Id: photoID}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.trips.GetTripPhoto(ctx, tripsv1.GetTripPhotoRequest_builder{Id: photoID, Size: 7}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}

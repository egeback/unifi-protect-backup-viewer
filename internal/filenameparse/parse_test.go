package filenameparse

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		filename   string
		wantCamera string
		wantStart  string
		wantEnd    string
	}{
		{
			filename:   "Baksidan - Frrdet 8-8-2026, 2.04.06pm GMT+2 - 8-8-2026, 2.04.27pm GMT+2.mp4",
			wantCamera: "Baksidan - Frrdet",
			wantStart:  "2026-08-08T14:04:06+02:00",
			wantEnd:    "2026-08-08T14:04:27+02:00",
		},
		{
			filename:   "G6 Bullet 8-5-2026, 10.00.06pm GMT+2 - 8-5-2026, 10.56.36pm GMT+2.mp4",
			wantCamera: "G6 Bullet",
			wantStart:  "2026-08-05T22:00:06+02:00",
			wantEnd:    "2026-08-05T22:56:36+02:00",
		},
		{
			filename:   "Baksidan - Frrdet 8-8-2026, 11.36.21am GMT+2 - 8-8-2026, 11.36.41am GMT+2.mp4",
			wantCamera: "Baksidan - Frrdet",
			wantStart:  "2026-08-08T11:36:21+02:00",
			wantEnd:    "2026-08-08T11:36:41+02:00",
		},
		{
			filename:   "Framsidan 8-9-2026, 12.10.14pm GMT+2 - 8-9-2026, 12.10.38pm GMT+2.mp4",
			wantCamera: "Framsidan",
			wantStart:  "2026-08-09T12:10:14+02:00",
			wantEnd:    "2026-08-09T12:10:38+02:00",
		},
	}

	for _, c := range cases {
		got, err := Parse(c.filename)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", c.filename, err)
		}
		if got.CameraName != c.wantCamera {
			t.Errorf("Parse(%q).CameraName = %q, want %q", c.filename, got.CameraName, c.wantCamera)
		}
		if got.Start.Format("2006-01-02T15:04:05-07:00") != c.wantStart {
			t.Errorf("Parse(%q).Start = %v, want %v", c.filename, got.Start, c.wantStart)
		}
		if got.End.Format("2006-01-02T15:04:05-07:00") != c.wantEnd {
			t.Errorf("Parse(%q).End = %v, want %v", c.filename, got.End, c.wantEnd)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse("not-a-clip.mp4"); err == nil {
		t.Fatal("expected error for non-matching filename")
	}
}

func TestNormalizeCameraName(t *testing.T) {
	a := NormalizeCameraName("Baksidan - Förrådet")
	b := NormalizeCameraName("Baksidan - Frrdet")
	if a != b {
		t.Fatalf("normalized names differ: %q vs %q", a, b)
	}
	if a != "baksidanfrrdet" {
		t.Fatalf("unexpected normalized form: %q", a)
	}
}

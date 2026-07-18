package store

import "testing"

func TestDerivedWatermarkRoundTrip(t *testing.T) {
	paths := PathsForHome(t.TempDir())

	// Absent file: present=false, no error.
	if hash, present, err := ReadDerivedWatermark(paths); err != nil || present || hash != "" {
		t.Fatalf("absent watermark: got (%q,%v,%v), want (\"\",false,nil)", hash, present, err)
	}

	const want = "0123456789abcdef0123456789abcdef01234567"
	if err := WriteDerivedWatermark(paths, want); err != nil {
		t.Fatalf("WriteDerivedWatermark: %v", err)
	}
	hash, present, err := ReadDerivedWatermark(paths)
	if err != nil || !present || hash != want {
		t.Fatalf("round trip: got (%q,%v,%v), want (%q,true,nil)", hash, present, err, want)
	}

	// Empty hash is refused at the write boundary.
	if err := WriteDerivedWatermark(paths, ""); err == nil {
		t.Fatal("WriteDerivedWatermark(\"\") succeeded; want contract error")
	}
}

package discord

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestDefaultAutoArchiveDurationValidation(t *testing.T) {
	s := defaultAutoArchiveDurationSchema()
	for _, ok := range autoArchiveDurations {
		if _, errs := s.ValidateFunc(ok, "default_auto_archive_duration"); len(errs) != 0 {
			t.Errorf("%d should be accepted, got %v", ok, errs)
		}
	}
	for _, bad := range []int{0, 30, 1441, 20160} {
		if _, errs := s.ValidateFunc(bad, "default_auto_archive_duration"); len(errs) == 0 {
			t.Errorf("%d should be rejected", bad)
		}
	}
}

func TestReadDefaultAutoArchiveDuration(t *testing.T) {
	res := resourceDiscordTextChannel()

	d := schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{})
	if err := readDefaultAutoArchiveDuration([]byte(`{"id":"1","type":0,"default_auto_archive_duration":10080}`), d); err != nil {
		t.Fatal(err)
	}
	if got := d.Get("default_auto_archive_duration").(int); got != 10080 {
		t.Errorf("expected 10080, got %d", got)
	}

	// A channel object without the field must not overwrite what state holds.
	d = schema.TestResourceDataRaw(t, res.Schema, map[string]interface{}{"default_auto_archive_duration": 4320})
	if err := readDefaultAutoArchiveDuration([]byte(`{"id":"1","type":0}`), d); err != nil {
		t.Fatal(err)
	}
	if got := d.Get("default_auto_archive_duration").(int); got != 4320 {
		t.Errorf("expected 4320 to survive, got %d", got)
	}
}

func TestChannelHasThreads(t *testing.T) {
	for typ, want := range map[string]bool{"text": true, "news": true, "forum": true, "voice": false, "category": false} {
		if got := channelHasThreads(typ); got != want {
			t.Errorf("%s: expected %v, got %v", typ, want, got)
		}
	}
}

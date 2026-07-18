package autogit

import (
	"fmt"
	"time"
)

// Lifecycle-tag naming (ARCHITECTURE.md "Git tags for project/thread
// lifecycle navigation", shipped this wave). A lifecycle tag marks a
// thread/project life event at the primary commit that first captured
// it. The ref namespace is:
//
//	<kind>/<id>/<event>_<stamp>
//
// e.g. thread/thr_42/created_2026-05-08T03-12-00Z,
//      thread/thr_42/retired_...,  thread/thr_42/archived_...,
//      project/prj_3/created_...
//
// This file is the SINGLE source for the stamp format and the ref name —
// nothing else hand-formats a lifecycle tag or its stamp, so the two
// stay in lockstep with ParseTagStamp.

// Tag kinds and events — the greppable vocabulary the barrier derives.
const (
	TagKindThread  = "thread"
	TagKindProject = "project"

	TagEventCreated  = "created"
	TagEventRetired  = "retired"
	TagEventArchived = "archived"
)

// tagStampLayout is the RFC3339 instant with its time colons rewritten to
// hyphens at fixed positions (git ref names forbid ':'). The trailing 'Z'
// is a literal — the stamp is always UTC-normalized, so the layout is
// unambiguous and lexically sortable: string order over stamps equals
// chronological order over the underlying instants, regardless of the
// original offsets or DST.
const tagStampLayout = "2006-01-02T15-04-05Z"

// TagStamp renders t as the UTC-normalized, lexically-sortable lifecycle
// stamp. The instant is the event's own time (from the §2.8 log line), so
// stamps are timestamp-unique by construction.
func TagStamp(t time.Time) string {
	return t.UTC().Format(tagStampLayout)
}

// ParseTagStamp inverts TagStamp, returning the UTC instant the stamp
// encodes.
func ParseTagStamp(stamp string) (time.Time, error) {
	t, err := time.Parse(tagStampLayout, stamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("autogit.ParseTagStamp %q: %w", stamp, err)
	}
	return t, nil
}

// LifecycleTagName composes the <kind>/<id>/<event>_<stamp> ref name.
func LifecycleTagName(kind, id, event string, t time.Time) string {
	return kind + "/" + id + "/" + event + "_" + TagStamp(t)
}

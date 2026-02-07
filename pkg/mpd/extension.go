package mpd

import (
	m "github.com/Eyevinn/dash-mpd/mpd"
)

// SegmentTemplate returns the segment template of the AdaptationSet or nil if not set.
func SegmentTemplate(a *m.AdaptationSetType) *m.SegmentTemplateType {
	if a.SegmentTemplate != nil {
		return a.SegmentTemplate
	}
	if len(a.Representations) > 0 && a.Representations[0].SegmentTemplate != nil {
		return a.Representations[0].SegmentTemplate
	}
	return nil
}

// ReprSegmentTemplate returns the segment template of the RepresentationType or nil if not set.
func ReprSegmentTemplate(m *m.RepresentationType) *m.SegmentTemplateType {
	if m.SegmentTemplate != nil {
		return m.SegmentTemplate
	}
	if m.Parent() != nil && m.Parent().SegmentTemplate != nil {
		return m.Parent().SegmentTemplate
	}
	return nil
}

package model

import "time"

// Observation time is internal provenance, not a source modification timestamp.
// Cache hits retain the time of the original network read. This prevents older
// cached lists from rolling back a subsequently confirmed derivative revision.
func ObjObservedAt(obj Obj) time.Time {
	for obj != nil {
		if observed, ok := obj.(interface{ ObservationTime() time.Time }); ok {
			return observed.ObservationTime()
		}
		if wrapped, ok := obj.(ObjUnwrap); ok {
			obj = wrapped.Unwrap()
		} else {
			break
		}
	}
	return time.Time{}
}
func StampObjObservation(obj Obj, at time.Time) Obj {
	if named, ok := obj.(*ObjWrapName); ok {
		copy := *named
		copy.ObservedAt = at
		return &copy
	}
	return &ObjWrapName{Obj: obj, Name: obj.GetName(), ObservedAt: at}
}
func (o *ObjWrapName) ObservationTime() time.Time { return o.ObservedAt }

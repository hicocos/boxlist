package iconlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

var transparencyProcessor = make(chan struct{}, 1)

// TransparencyJob is a bounded, latest-result-only view; source/output bytes are
// never retained in job history. The catalog remains the durable source of truth.
type TransparencyJob struct {
	AssetID  string `json:"asset_id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Message  string `json:"message,omitempty"`
	Asset    *Asset `json:"asset,omitempty"`
}

func (s *Service) handleTransparency(w http.ResponseWriter, r *http.Request, asset string) error {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return fault(405)
	}
	var owner string
	var err error
	if r.Method == http.MethodGet {
		// Same-origin GET fetches omit Origin. Reads still require native admin auth;
		// only mutations require the additional exact-Origin CSRF check.
		owner, err = s.hooks.Authorize(r)
		if err == nil && !identityPattern.MatchString(owner) {
			err = fault(401)
		}
	} else {
		owner, err = s.authorize(r)
	}
	if err != nil {
		return err
	}
	if r.Header.Get("Expect") != "" {
		return fault(417)
	}
	if r.Method == http.MethodGet {
		if r.ContentLength != 0 || (r.Header.Get("Content-Length") != "" && r.Header.Get("Content-Length") != "0") {
			return fault(400)
		}
		s.jobsMu.Lock()
		job, ok := s.jobs[asset]
		s.jobsMu.Unlock()
		if !ok {
			return fault(404)
		}
		reply(w, 200, map[string]any{"code": 200, "data": job})
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fault(415)
	}
	if r.ContentLength > 1024 {
		return fault(413)
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(20 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var input struct {
		Revision string `json:"revision"`
	}
	if err = decoder.Decode(&input); err != nil {
		return fault(400)
	}
	if decoder.Decode(new(any)) != io.EOF || !validRevision(input.Revision) {
		return fault(400)
	}
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.closing {
		return fault(503)
	}
	if s.activeJob != "" {
		if s.activeJob != asset {
			return fault(429)
		}
		reply(w, 200, map[string]any{"code": 200, "data": s.jobs[asset]})
		return nil
	}
	select {
	case transparencyProcessor <- struct{}{}:
	default:
		return fault(429)
	}
	current, data, err := s.store.transparencyInput(asset, input.Revision)
	if err != nil {
		<-transparencyProcessor
		return err
	}
	// Only real catalog IDs enter the map, and tombstones count toward MaxAssets.
	job := TransparencyJob{AssetID: asset, Status: "processing", Progress: 0}
	s.jobs[asset], s.activeJob = job, asset
	// Keep authentication values while discarding the handler's cancellation and
	// deadline. Native hooks impose their own bounded authorization timeouts.
	request := r.Clone(context.WithoutCancel(r.Context()))
	request.Body, request.GetBody = nil, nil
	s.workers.Add(1)
	go s.runTransparency(request, owner, current, data)
	reply(w, 200, map[string]any{"code": 200, "data": job})
	return nil
}

func (s *Service) runTransparency(r *http.Request, owner string, asset Asset, data []byte) {
	defer s.workers.Done()
	id := asset.ID
	job := TransparencyJob{AssetID: asset.ID, Status: "failed", Progress: 100}
	defer func() {
		if recover() != nil {
			job.Status, job.Message, job.Asset = "failed", safeMessage(500), nil
		}
		s.jobsMu.Lock()
		<-transparencyProcessor
		s.jobs[id] = job
		s.activeJob = ""
		s.jobsMu.Unlock()
	}()
	output, changed, err := transparentGIF(data, func(progress int) {
		s.jobsMu.Lock()
		current := s.jobs[asset.ID]
		current.Progress = progress
		s.jobs[asset.ID] = current
		s.jobsMu.Unlock()
	})
	if err == nil {
		func() {
			s.mutation.Lock()
			defer s.mutation.Unlock()
			if changed {
				asset, err = s.store.replaceGIF(id, asset.Revision, output, func() error { return s.reauthorize(r, owner) })
			} else {
				err = s.reauthorize(r, owner)
				if err == nil {
					asset, _, err = s.store.transparencyInput(id, asset.Revision)
				}
			}
		}()
	}
	if err != nil {
		status := 500
		var f Fault
		var fp *Fault
		if errors.As(err, &f) {
			status = f.Status
		} else if errors.As(err, &fp) {
			status = fp.Status
		}
		job.Message = safeMessage(status)
		if status == 503 {
			job.Message = "素材库暂不可用，处理结果尚未确认，请刷新素材库核对"
		}
		return
	}
	job.Status, job.Asset = "unchanged", &asset
	if changed {
		job.Status = "done"
	}
}

package telemetry

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CollectionHTTPSource struct {
	origin string
	client *http.Client
}

func NewCollectionSource(origin string) (*CollectionHTTPSource, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return nil, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "fixture" && (ip == nil || !ip.IsLoopback()) {
		return nil, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	if port, e := strconv.Atoi(u.Port()); e != nil || port < 1 || port > 65535 {
		return nil, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, DisableCompression: true, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 8192}
	return &CollectionHTTPSource{origin, &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *CollectionHTTPSource) FetchCollection(ctx context.Context, i domain.CollectionIntent) (domain.CollectionRawCapture, error) {
	if i.FixtureOrigin != s.origin {
		return domain.CollectionRawCapture{}, domain.NewError(domain.Invalid, "invalid_capture")
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	query := url.Values{"protocol": {"collection-fixture-v1"}, "fixture_id": {i.Fixture.ID}, "well_id": {i.Binding.ExternalID}, "start": {i.Start.UTC().Format(time.RFC3339)}, "end": {i.End.UTC().Format(time.RFC3339)}}
	req, e := http.NewRequestWithContext(bounded, http.MethodGet, s.origin+"/v1/collection-capture?"+query.Encode(), nil)
	if e != nil {
		return domain.CollectionRawCapture{}, domain.NewError(domain.Invalid, "invalid_capture")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	response, e := s.client.Do(req)
	if e != nil {
		return domain.CollectionRawCapture{}, collectionHTTPError(e)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		kind := domain.Invalid
		switch response.StatusCode {
		case 429, 502, 503, 504:
			kind = domain.Unavailable
		}
		return domain.CollectionRawCapture{}, domain.NewError(kind, "source_unavailable")
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || response.Header.Get("Content-Encoding") != "" {
		return domain.CollectionRawCapture{}, domain.NewError(domain.Invalid, "invalid_capture")
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, 2097153))
	if len(body) > 2097152 {
		return domain.CollectionRawCapture{}, domain.NewError(domain.Invalid, "input_limit")
	}
	if e != nil {
		return domain.CollectionRawCapture{}, collectionHTTPError(e)
	}
	return domain.CollectionRawCapture{Body: body, ReceivedAt: time.Now().UTC()}, nil
}
func collectionHTTPError(e error) error {
	var timeout net.Error
	if errors.Is(e, context.DeadlineExceeded) || (errors.As(e, &timeout) && timeout.Timeout()) {
		return domain.NewError(domain.Unavailable, "source_timeout")
	}
	return domain.NewError(domain.Unavailable, "source_unavailable")
}

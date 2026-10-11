package derivatives

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const s3MaxObjectBytes int64 = 32 << 20
const s3RequestTimeout = 20 * time.Second

var s3BucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var s3RegionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,63}$`)
var s3ProbeIDPattern = regexp.MustCompile(`^[0-9a-f]{32}\.bin$`)

func normalizeS3Target(r S3ConfigRequest) (*DerivativeS3Target, error) {
	if len(r.Endpoint) > 2048 || strings.ContainsAny(r.Endpoint, "\\\x00\r\n\t ") {
		return nil, ErrS3ConfigurationInvalid
	}
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(r.Endpoint, "#") || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, ErrS3ConfigurationInvalid
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.ContainsAny(host, "%/") || s3MetadataHost(host) {
		return nil, ErrS3ConfigurationInvalid
	}
	if port := u.Port(); port != "" {
		p, e := strconv.Atoi(port)
		if e != nil || p < 1 || p > 65535 {
			return nil, ErrS3ConfigurationInvalid
		}
	}
	if ip := net.ParseIP(host); ip != nil && !s3AllowedAddress(ip, u.Scheme == "http") {
		return nil, ErrS3ConfigurationInvalid
	}
	if !s3BucketPattern.MatchString(r.Bucket) || strings.Contains(r.Bucket, "..") || strings.Contains(r.Bucket, ".-") || strings.Contains(r.Bucket, "-.") || net.ParseIP(r.Bucket) != nil || !s3RegionPattern.MatchString(r.Region) {
		return nil, ErrS3ConfigurationInvalid
	}
	prefix := r.Prefix
	if len(prefix) > 512 || strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix, "\\\x00\r\n\t%?#") {
		return nil, ErrS3ConfigurationInvalid
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		for _, p := range strings.Split(prefix, "/") {
			if p == "" || p == "." || p == ".." || strings.TrimSpace(p) != p {
				return nil, ErrS3ConfigurationInvalid
			}
		}
		prefix += "/"
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	cdn, err := normalizeS3CDN(r.CDNURL)
	if err != nil || (r.PublicCDN && cdn == "") {
		return nil, ErrS3ConfigurationInvalid
	}
	return &DerivativeS3Target{Endpoint: u.String(), Bucket: r.Bucket, Region: r.Region, Prefix: prefix, PathStyle: r.PathStyle, CDNURL: cdn, PublicCDN: r.PublicCDN}, nil
}

func normalizeS3CDN(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 || strings.ContainsAny(raw, "\\\x00\r\n\t ") {
		return "", ErrS3ConfigurationInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || s3MetadataHost(u.Hostname()) {
		return "", ErrS3ConfigurationInvalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		if !s3AllowedAddress(ip, false) {
			return "", ErrS3ConfigurationInvalid
		}
	} else {
		if len(host) > 253 {
			return "", ErrS3ConfigurationInvalid
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", ErrS3ConfigurationInvalid
			}
			for _, c := range label {
				if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
					return "", ErrS3ConfigurationInvalid
				}
			}
		}
	}
	if port := u.Port(); port != "" {
		p, e := strconv.Atoi(port)
		if e != nil || p < 1 || p > 65535 {
			return "", ErrS3ConfigurationInvalid
		}
	}
	if strings.ContainsAny(u.Path, "\\\x00\r\n\t") || strings.Contains(u.Path, "//") {
		return "", ErrS3ConfigurationInvalid
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." || strings.Contains(part, "%") {
			return "", ErrS3ConfigurationInvalid
		}
	}
	// Refuse escaped alternate slash/dot identities rather than decoding twice.
	if u.RawPath != "" {
		return "", ErrS3ConfigurationInvalid
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}

func s3CDNObjectURL(target *DerivativeS3Target, key string) string {
	if target == nil || !target.PublicCDN || !s3OwnedKey(target, key) {
		return ""
	}
	base, err := normalizeS3CDN(target.CDNURL)
	if err != nil || base == "" {
		return ""
	}
	u, _ := url.Parse(base)
	u.Path += "/" + key
	u.RawPath = ""
	return u.String()
}

// Only call after local ACL/ticket authorization. Explicit PublicCDN opt-in
// changes delivery privacy: anyone holding this URL can bypass later local ACLs.
func (m *Manager) PublicArtifactURL(a Artifact) string {
	t, err := m.remoteTarget(a)
	if err != nil {
		return ""
	}
	return s3CDNObjectURL(t, a.ObjectKey)
}

func s3CDNProbe(ctx context.Context, target *DerivativeS3Target, key string, expected []byte) error {
	address := s3CDNObjectURL(target, key)
	if address == "" {
		return ErrS3CDNProbeFailed
	}
	transport := &http.Transport{Proxy: nil, DialContext: s3DialContext(false), TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: s3RequestTimeout, MaxResponseHeaderBytes: 64 << 10, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: s3RequestTimeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return ErrS3CDNProbeFailed
	}
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request)
	if err != nil {
		return ErrS3CDNProbeFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrS3CDNProbeFailed
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(len(expected))+1))
	if err != nil || !bytes.Equal(body, expected) {
		return ErrS3CDNProbeFailed
	}
	return nil
}

func s3MetadataHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "metadata.google.internal" || host == "metadata.google" || host == "instance-data" || host == "instance-data.ec2.internal" || host == "metadata.azure.internal"
}

func s3AllowedAddress(ip net.IP, plainHTTP bool) bool {
	// Block metadata/link-local even when the admin explicitly configured it.
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.Equal(net.ParseIP("100.100.100.200")) || ip.Equal(net.ParseIP("168.63.129.16")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	return !plainHTTP && ip.IsGlobalUnicast()
}

// No environment proxies; validate EVERY DNS answer at the actual socket dial
// (including virtual-host buckets), and pin the dial to the checked address.
// HTTP is allowed only for explicit private/loopback admin S3 endpoints.
func s3DialContext(plainHTTP bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || s3MetadataHost(host) {
			return nil, ErrS3ConfigurationInvalid
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, ErrS3ConfigurationInvalid
		}
		for _, ip := range ips {
			if ip.Zone != "" || !s3AllowedAddress(ip.IP, plainHTTP) {
				return nil, ErrS3ConfigurationInvalid
			}
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, ErrS3ConfigurationInvalid
	}
}

type s3BoundedTransport struct {
	transport            *http.Transport
	endpointHost, bucket string
	pathStyle            bool
}
type s3LimitedBody struct {
	io.Reader
	io.Closer
}

func (t *s3BoundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := strings.ToLower(r.URL.Host)
	if host != t.endpointHost && (t.pathStyle || host != strings.ToLower(t.bucket)+"."+t.endpointHost) {
		return nil, ErrS3ConfigurationInvalid
	}
	response, err := t.transport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		response.Body = &s3LimitedBody{Reader: io.LimitReader(response.Body, 64<<10), Closer: response.Body}
	}
	return response, nil
}

func (m *Manager) s3Client(t *DerivativeS3Target) (*s3.S3, func(), error) {
	if t == nil {
		return nil, nil, ErrS3TargetUnavailable
	}
	checked, err := normalizeS3Target(S3ConfigRequest{Endpoint: t.Endpoint, Bucket: t.Bucket, Region: t.Region, Prefix: t.Prefix, PathStyle: t.PathStyle})
	if err != nil {
		return nil, nil, err
	}
	if t.ID != 0 && (t.IdentityHash != s3Identity(checked)) {
		return nil, nil, ErrS3ConfigurationInvalid
	}
	access, secret, err := m.s3Credentials(t)
	if err != nil {
		return nil, nil, err
	}
	u, _ := url.Parse(checked.Endpoint)
	transport := &http.Transport{Proxy: nil, DialContext: s3DialContext(u.Scheme == "http"), TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: s3RequestTimeout, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 15 * time.Second, MaxResponseHeaderBytes: 64 << 10, DisableCompression: true}
	httpClient := &http.Client{Timeout: s3RequestTimeout, Transport: &s3BoundedTransport{transport: transport, endpointHost: strings.ToLower(u.Host), bucket: checked.Bucket, pathStyle: checked.PathStyle}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cfg := aws.NewConfig().WithEndpoint(checked.Endpoint).WithRegion(checked.Region).WithCredentials(credentials.NewStaticCredentials(access, secret, "")).WithHTTPClient(httpClient).WithMaxRetries(0).WithS3ForcePathStyle(checked.PathStyle).WithDisableSSL(u.Scheme == "http")
	cfg.S3Disable100Continue = aws.Bool(true)
	sess, err := session.NewSession(cfg)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, ErrS3ConfigurationInvalid
	}
	return s3.New(sess), transport.CloseIdleConnections, nil
}

func s3ArtifactKey(t *DerivativeS3Target, id string) (string, error) {
	if t == nil || !validArtifactID(id) {
		return "", ErrS3ConfigurationInvalid
	}
	return t.Prefix + "artifacts/" + id[:2] + "/" + id + ".webp", nil
}

// Only derivative content-addressed keys and this module's synthetic probe keys
// are accepted. No arbitrary-key deletion, list operations or bucket cleanup.
func s3OwnedKey(t *DerivativeS3Target, key string) bool {
	if t == nil || !strings.HasPrefix(key, t.Prefix) {
		return false
	}
	rel := strings.TrimPrefix(key, t.Prefix)
	if strings.HasPrefix(rel, ".boxlist-probes/") {
		return s3ProbeIDPattern.MatchString(strings.TrimPrefix(rel, ".boxlist-probes/"))
	}
	parts := strings.Split(rel, "/")
	if len(parts) != 3 || parts[0] != "artifacts" || !strings.HasSuffix(parts[2], ".webp") {
		return false
	}
	id := strings.TrimSuffix(parts[2], ".webp")
	return validArtifactID(id) && parts[1] == id[:2]
}

func s3ValidDigest(sha string) bool {
	decoded, err := hex.DecodeString(sha)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(sha) == sha
}
func s3Missing(err error) bool {
	var requestFailure awserr.RequestFailure
	if errors.As(err, &requestFailure) && requestFailure.StatusCode() == http.StatusNotFound {
		return true
	}
	var awsError awserr.Error
	return errors.As(err, &awsError) && (awsError.Code() == "NoSuchKey" || awsError.Code() == "NotFound")
}

func (m *Manager) s3Put(ctx context.Context, target *DerivativeS3Target, key, path string, expectedBytes int64, sha string) error {
	if !s3OwnedKey(target, key) || expectedBytes <= 0 || expectedBytes > s3MaxObjectBytes || !s3ValidDigest(sha) {
		return ErrS3UploadFailed
	}
	if noSymlinks(path) != nil {
		return ErrS3UploadFailed
	}
	f, err := os.Open(path)
	if err != nil {
		return ErrS3UploadFailed
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != expectedBytes {
		return ErrS3UploadFailed
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, expectedBytes+1))
	if err != nil || n != expectedBytes || hex.EncodeToString(h.Sum(nil)) != sha {
		return ErrS3UploadFailed
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ErrS3UploadFailed
	}
	client, closeClient, err := m.s3Client(target)
	if err != nil {
		return err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(ctx, s3RequestTimeout)
	defer cancel()
	_, err = client.PutObjectWithContext(ctx, &s3.PutObjectInput{Bucket: aws.String(target.Bucket), Key: aws.String(key), Body: f, ContentLength: aws.Int64(expectedBytes), ContentType: aws.String("image/webp"), CacheControl: aws.String("private, no-cache"), Metadata: map[string]*string{"sha256": aws.String(sha)}})
	if err != nil {
		return ErrS3UploadFailed
	}
	if e := m.s3Head(ctx, target, key, expectedBytes); e != nil {
		return ErrS3UploadFailed
	}
	return nil
}

func (m *Manager) s3Get(ctx context.Context, target *DerivativeS3Target, key string, expectedBytes int64, sha string) ([]byte, error) {
	if !s3OwnedKey(target, key) || expectedBytes <= 0 || expectedBytes > s3MaxObjectBytes || !s3ValidDigest(sha) {
		return nil, ErrS3ReadFailed
	}
	client, closeClient, err := m.s3Client(target)
	if err != nil {
		return nil, err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(ctx, s3RequestTimeout)
	defer cancel()
	object, err := client.GetObjectWithContext(ctx, &s3.GetObjectInput{Bucket: aws.String(target.Bucket), Key: aws.String(key)})
	if err != nil {
		if s3Missing(err) {
			return nil, ErrS3ObjectMissing
		}
		return nil, ErrS3ReadFailed
	}
	if object.Body == nil {
		return nil, ErrS3ReadFailed
	}
	defer object.Body.Close()
	if object.ContentLength == nil || *object.ContentLength != expectedBytes {
		return nil, ErrS3ReadFailed
	}
	body, err := io.ReadAll(io.LimitReader(object.Body, expectedBytes+1))
	if err != nil || int64(len(body)) != expectedBytes {
		return nil, ErrS3ReadFailed
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != sha {
		return nil, ErrS3ChecksumMismatch
	}
	return body, nil
}

func (m *Manager) s3Head(ctx context.Context, target *DerivativeS3Target, key string, expectedBytes int64) error {
	if !s3OwnedKey(target, key) || expectedBytes <= 0 || expectedBytes > s3MaxObjectBytes {
		return ErrS3ReadFailed
	}
	client, closeClient, err := m.s3Client(target)
	if err != nil {
		return err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(ctx, s3RequestTimeout)
	defer cancel()
	object, err := client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{Bucket: aws.String(target.Bucket), Key: aws.String(key)})
	if err != nil {
		if s3Missing(err) {
			return ErrS3ObjectMissing
		}
		return ErrS3ReadFailed
	}
	if object.ContentLength == nil || *object.ContentLength != expectedBytes {
		return ErrS3ReadFailed
	}
	return nil
}

func (m *Manager) s3Delete(ctx context.Context, target *DerivativeS3Target, key string) error {
	if !s3OwnedKey(target, key) {
		return ErrS3DeleteFailed
	}
	client, closeClient, err := m.s3Client(target)
	if err != nil {
		return err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(ctx, s3RequestTimeout)
	defer cancel()
	_, err = client.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{Bucket: aws.String(target.Bucket), Key: aws.String(key)})
	if err != nil && !s3Missing(err) {
		return ErrS3DeleteFailed
	}
	return nil
}

func (m *Manager) s3Probe(ctx context.Context, target *DerivativeS3Target) error {
	id, body := make([]byte, 16), make([]byte, 128)
	if _, err := rand.Read(id); err != nil {
		return ErrS3ProbeFailed
	}
	if _, err := rand.Read(body); err != nil {
		return ErrS3ProbeFailed
	}
	key := target.Prefix + ".boxlist-probes/" + hex.EncodeToString(id) + ".bin"
	client, closeClient, err := m.s3Client(target)
	if err != nil {
		return err
	}
	defer closeClient()
	probeCtx, cancel := context.WithTimeout(ctx, s3RequestTimeout)
	defer cancel()
	// Cleanup even after a timed-out PUT: a provider may have committed it.
	_, putErr := client.PutObjectWithContext(probeCtx, &s3.PutObjectInput{Bucket: aws.String(target.Bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String("application/octet-stream"), CacheControl: aws.String("private, no-store")})
	checkErr := putErr
	if checkErr == nil {
		digest := sha256.Sum256(body)
		_, checkErr = m.s3Get(probeCtx, target, key, int64(len(body)), hex.EncodeToString(digest[:]))
		if checkErr == nil {
			checkErr = m.s3Head(probeCtx, target, key, int64(len(body)))
		}
		if checkErr == nil && target.PublicCDN {
			checkErr = s3CDNProbe(probeCtx, target, key, body)
		}
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), s3RequestTimeout)
	defer cleanupCancel()
	deleteErr := m.s3Delete(cleanupCtx, target, key)
	// Read back the exact target to confirm deletion, not merely a 2xx DELETE.
	if deleteErr == nil {
		if headErr := m.s3Head(cleanupCtx, target, key, int64(len(body))); !errors.Is(headErr, ErrS3ObjectMissing) {
			deleteErr = ErrS3DeleteFailed
		}
	}
	if errors.Is(checkErr, ErrS3CDNProbeFailed) {
		return ErrS3CDNProbeFailed
	}
	if checkErr != nil || deleteErr != nil {
		return ErrS3ProbeFailed
	}
	return nil
}

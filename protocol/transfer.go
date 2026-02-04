package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
)

const (
	endpointPrefix         = "/api/0"
	bundleSuggestEndpoint  = endpointPrefix + "/suggest"
	bundleUploadEndpoint   = endpointPrefix + "/bundle"
	missingUploadEndpoint  = endpointPrefix + "/missing"
	finalizeUploadEndpoint = endpointPrefix + "/finalize"
	bundleDownloadEndpoint = endpointPrefix + "/dl/bundle"
	partsDownloadEndpoint  = endpointPrefix + "/dl/parts"
	listTeamsEndpoint      = endpointPrefix + "/teams"
)

// DiscoveryInfo holds the response from /.well-known/web-publication-protocol
type DiscoveryInfo struct {
	Protocols           []string `json:"protocols"`
	URL                 string   `json:"url"`
	APIKeyManagementURL string   `json:"apiKeyManagementUrl"`
}

// Discover fetches the xmit discovery info from XMIT_URL (default: https://xmit.co)
func Discover() (*DiscoveryInfo, error) {
	baseURL := os.Getenv("XMIT_URL")
	if baseURL == "" {
		baseURL = "https://xmit.co"
	}
	discoveryURL := baseURL + "/.well-known/web-publication-protocol"
	resp, err := http.Get(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch discovery info from %s: %w", discoveryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("discovery endpoint returned status %d", resp.StatusCode)
	}

	var info DiscoveryInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode discovery info: %w", err)
	}

	// Validate that xmit/0 protocol is supported
	supported := false
	for _, p := range info.Protocols {
		if p == "xmit/0" {
			supported = true
			break
		}
	}
	if !supported {
		return nil, fmt.Errorf("domain does not support xmit/0 protocol")
	}

	return &info, nil
}

type Request struct {
	Key    string `cbor:"1,keyasint"`
	Team   string `cbor:"2,keyasint,omitempty"`
	Domain string `cbor:"5,keyasint,omitempty"`
}

type Response struct {
	Success  bool     `cbor:"1,keyasint"`
	Errors   []string `cbor:"2,keyasint,omitempty"`
	Warnings []string `cbor:"3,keyasint,omitempty"`
	Messages []string `cbor:"4,keyasint,omitempty"`
}

type BundleSuggestRequest struct {
	Request
	ID Hash `cbor:"6,keyasint,omitempty"`
}

type BundleSuggestResponse struct {
	Response
	Present bool   `cbor:"5,keyasint,omitempty"`
	Missing []Hash `cbor:"6,keyasint,omitempty"`
}

type BundleUploadRequest struct {
	Request
	Bundle []byte `cbor:"6,keyasint,omitempty"`
}

type BundleUploadResponse struct {
	Response
	ID      Hash   `cbor:"5,keyasint,omitempty"`
	Missing []Hash `cbor:"6,keyasint,omitempty"`
}

type MissingUploadRequest struct {
	Request
	ID    Hash     `cbor:"6,keyasint,omitempty"`
	Parts [][]byte `cbor:"7,keyasint,omitempty"`
}

type MissingUploadResponse struct {
	Response
}

type FinalizeUploadRequest struct {
	Request
	ID Hash `cbor:"6,keyasint,omitempty"`
}

type FinalizeUploadResponse struct {
	Response
}

type BundleDownloadRequest struct {
	Request
	ID string `cbor:"6,keyasint,omitempty"`
}

type BundleDownloadResponse struct {
	Response
	Bundle []byte `cbor:"5,keyasint,omitempty"`
}

type PartsDownloadRequest struct {
	Request
	Hashes []Hash `cbor:"6,keyasint,omitempty"`
}

type PartsDownloadResponse struct {
	Response
	Parts [][]byte `cbor:"5,keyasint,omitempty"`
}

type ListTeamsRequest struct {
	Request
}

type Team struct {
	ID   string `cbor:"1,keyasint,omitempty"`
	Name string `cbor:"2,keyasint,omitempty"`
}

type ListTeamsResponse struct {
	Response
	Teams         []Team `cbor:"5,keyasint,omitempty"`
	ManagementURL string `cbor:"6,keyasint,omitempty"`
}

type RequestKeyRequest struct {
	Name string `cbor:"1,keyasint,omitempty"`
}

type RequestKeyResponse struct {
	Response
	BrowserURL string `cbor:"5,keyasint,omitempty"`
	PollURL    string `cbor:"6,keyasint,omitempty"`
	Secret     string `cbor:"7,keyasint,omitempty"`
	RequestID  string `cbor:"8,keyasint,omitempty"`
}

// resolveClients resolves a URL to multiple IPs and creates an HTTP client for each
func resolveClients(baseURL string) ([]*http.Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup IPs for %s: %w", host, err)
	}

	// Filter to IPv4 addresses only for simplicity
	var ipv4s []net.IP
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			ipv4s = append(ipv4s, ip4)
		}
	}
	if len(ipv4s) == 0 {
		ipv4s = ips
	}

	log.Printf("🌐 Resolved %s to %d IPs", host, len(ipv4s))

	clients := make([]*http.Client, len(ipv4s))
	for i, ip := range ipv4s {
		targetIP := ip.String()
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, net.JoinHostPort(targetIP, port))
			},
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		}
		clients[i] = &http.Client{Transport: transport}
	}
	return clients, nil
}

// encodeRequest encodes a request to CBOR and compresses it with zstd
func encodeRequest(encMode cbor.EncMode, req interface{}) ([]byte, error) {
	var b bytes.Buffer
	bf := bufio.NewWriter(&b)
	z, err := zstd.NewWriter(bf, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd writer: %w", err)
	}

	if err = encMode.NewEncoder(z).Encode(req); err != nil {
		z.Close()
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	if err = z.Close(); err != nil {
		return nil, fmt.Errorf("failed to close zstd writer: %w", err)
	}

	if err = bf.Flush(); err != nil {
		return nil, fmt.Errorf("failed to flush buffer: %w", err)
	}

	return b.Bytes(), nil
}

// decodeResponse decodes a CBOR+zstd response
func decodeResponse(body io.ReadCloser, resp interface{}) error {
	defer body.Close()

	zd, err := zstd.NewReader(body)
	if err != nil {
		return fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer zd.Close()

	if err = cbor.NewDecoder(zd).Decode(resp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// ParallelUploader manages parallel chunk uploads across multiple IPs
type ParallelUploader struct {
	clients   []*http.Client
	baseURL   string
	encMode   cbor.EncMode
	sendSem   chan struct{}
	clientIdx atomic.Uint64
}

// NewParallelUploader creates an uploader that spreads requests across IPs
func NewParallelUploader(baseURL string, concurrency int) (*ParallelUploader, error) {
	clients, err := resolveClients(baseURL)
	if err != nil {
		return nil, err
	}

	encMode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		return nil, fmt.Errorf("failed to create cbor encoder: %w", err)
	}

	return &ParallelUploader{
		clients: clients,
		baseURL: baseURL,
		encMode: encMode,
		sendSem: make(chan struct{}, concurrency),
	}, nil
}

// EncMode returns the CBOR encoding mode
func (p *ParallelUploader) EncMode() cbor.EncMode {
	return p.encMode
}

// ChunkUploadResult holds the result of a single chunk upload
type ChunkUploadResult struct {
	Index    int
	Response *MissingUploadResponse
	Err      error
}

// UploadChunksParallel uploads all chunks in parallel (max concurrency), starting in order
func (p *ParallelUploader) UploadChunksParallel(key, domain string, chunks [][][]byte) []ChunkUploadResult {
	results := make([]ChunkUploadResult, len(chunks))
	var wg sync.WaitGroup

	// Use a channel to ensure chunks start in order
	starts := make([]chan struct{}, len(chunks))
	for i := range starts {
		starts[i] = make(chan struct{})
	}

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, parts [][]byte) {
			defer wg.Done()
			// Wait for our turn to start
			<-starts[idx]
			resp, err := p.uploadChunk(key, domain, idx, len(chunks), parts, starts)
			results[idx] = ChunkUploadResult{
				Index:    idx,
				Response: resp,
				Err:      err,
			}
		}(i, chunk)
	}

	// Signal first chunk to start
	close(starts[0])

	wg.Wait()
	return results
}

func (p *ParallelUploader) uploadChunk(key, domain string, i, count int, parts [][]byte, starts []chan struct{}) (*MissingUploadResponse, error) {
	// Encode the request
	payload, err := encodeRequest(p.encMode, &MissingUploadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		Parts: parts,
	})
	if err != nil {
		// Signal next chunk to start even on error
		if i+1 < len(starts) {
			close(starts[i+1])
		}
		return nil, err
	}

	// Acquire semaphore for sending data
	p.sendSem <- struct{}{}

	// Signal next chunk to start (after we acquired semaphore)
	if i+1 < len(starts) {
		close(starts[i+1])
	}

	// Select client in round-robin fashion (after acquiring semaphore to spread load)
	clientIdx := int(p.clientIdx.Add(1)-1) % len(p.clients)
	client := p.clients[clientIdx]

	if len(parts) == 1 {
		log.Printf("🏃 Uploading chunk %d/%d of 1 missing part (%d bytes compressed) via IP #%d…", i+1, count, len(payload), clientIdx+1)
	} else {
		log.Printf("🏃 Uploading chunk %d/%d of %d missing parts (%d bytes compressed) via IP #%d…", i+1, count, len(parts), len(payload), clientIdx+1)
	}

	// Create semaphore reader that releases semaphore when body is fully sent
	bodyReader := &semaphoreReader{
		reader: bytes.NewReader(payload),
		sem:    p.sendSem,
	}

	// Create the request
	req, err := http.NewRequest("POST", p.baseURL+missingUploadEndpoint, bodyReader)
	if err != nil {
		bodyReader.ensureReleased()
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor+zstd")
	req.ContentLength = int64(len(payload))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}

	resp, err := client.Do(req)
	// Ensure semaphore is released if the reader didn't complete
	bodyReader.ensureReleased()
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", missingUploadEndpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, missingUploadEndpoint)
	}

	log.Printf("🧘 Chunk %d/%d upload complete, waiting for server…", i+1, count)

	var r MissingUploadResponse
	if err = decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}

	log.Printf("✅ Chunk %d/%d done", i+1, count)
	return &r, nil
}

// UploadBundle uploads the bundle using a round-robin client
func (p *ParallelUploader) UploadBundle(key, domain string, bundle []byte) (*BundleUploadResponse, error) {
	payload, err := encodeRequest(p.encMode, &BundleUploadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		Bundle: bundle,
	})
	if err != nil {
		return nil, err
	}

	// Select client in round-robin fashion
	clientIdx := int(p.clientIdx.Add(1)-1) % len(p.clients)
	client := p.clients[clientIdx]

	log.Printf("🚶 Uploading bundle (%d bytes) via IP #%d…", len(payload), clientIdx+1)

	req, err := http.NewRequest("POST", p.baseURL+bundleUploadEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor+zstd")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", bundleUploadEndpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, bundleUploadEndpoint)
	}

	log.Print("🧘 Bundle upload complete, waiting for server…")

	var r BundleUploadResponse
	if err = decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SuggestBundle suggests a bundle using a round-robin client
func (p *ParallelUploader) SuggestBundle(key, domain string, id Hash) (*BundleSuggestResponse, error) {
	payload, err := encodeRequest(p.encMode, &BundleSuggestRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		ID: id,
	})
	if err != nil {
		return nil, err
	}

	// Select client in round-robin fashion
	clientIdx := int(p.clientIdx.Add(1)-1) % len(p.clients)
	client := p.clients[clientIdx]

	log.Print("🤔 Suggesting bundle…")

	req, err := http.NewRequest("POST", p.baseURL+bundleSuggestEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor+zstd")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", bundleSuggestEndpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, bundleSuggestEndpoint)
	}

	var r BundleSuggestResponse
	if err = decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Finalize finalizes the upload using a round-robin client
func (p *ParallelUploader) Finalize(key, domain string, id Hash) (*FinalizeUploadResponse, error) {
	payload, err := encodeRequest(p.encMode, &FinalizeUploadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		ID: id,
	})
	if err != nil {
		return nil, err
	}

	// Select client in round-robin fashion
	clientIdx := int(p.clientIdx.Add(1)-1) % len(p.clients)
	client := p.clients[clientIdx]

	log.Print("🏁 Finalizing…")

	req, err := http.NewRequest("POST", p.baseURL+finalizeUploadEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor+zstd")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", finalizeUploadEndpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, finalizeUploadEndpoint)
	}

	var r FinalizeUploadResponse
	if err = decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// semaphoreReader wraps a reader and releases a semaphore when fully read
type semaphoreReader struct {
	reader   io.Reader
	sem      chan struct{}
	released atomic.Bool
}

func (r *semaphoreReader) Read(p []byte) (n int, err error) {
	n, err = r.reader.Read(p)
	if err == io.EOF {
		r.ensureReleased()
	}
	return
}

func (r *semaphoreReader) ensureReleased() {
	if r.released.CompareAndSwap(false, true) {
		<-r.sem
	}
}

// ParallelDownloader manages parallel downloads across multiple IPs
type ParallelDownloader struct {
	clients   []*http.Client
	baseURL   string
	encMode   cbor.EncMode
	sem       chan struct{} // semaphore for concurrent downloads
	clientIdx atomic.Uint64 // round-robin index for client selection
}

// NewParallelDownloader creates a downloader that spreads requests across IPs
func NewParallelDownloader(baseURL string, concurrency int) (*ParallelDownloader, error) {
	clients, err := resolveClients(baseURL)
	if err != nil {
		return nil, err
	}

	encMode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		return nil, fmt.Errorf("failed to create cbor encoder: %w", err)
	}

	return &ParallelDownloader{
		clients: clients,
		baseURL: baseURL,
		encMode: encMode,
		sem:     make(chan struct{}, concurrency),
	}, nil
}

// DownloadBundle downloads a bundle using a round-robin client
func (p *ParallelDownloader) DownloadBundle(key, domain, id string) (*BundleDownloadResponse, error) {
	payload, err := encodeRequest(p.encMode, &BundleDownloadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		ID: id,
	})
	if err != nil {
		return nil, err
	}

	clientIdx := int(p.clientIdx.Add(1)-1) % len(p.clients)
	client := p.clients[clientIdx]

	req, err := http.NewRequest("POST", p.baseURL+bundleDownloadEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor+zstd")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", bundleDownloadEndpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, bundleDownloadEndpoint)
	}

	var r BundleDownloadResponse
	if err = decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DownloadParts downloads parts using a round-robin client selection
func (p *ParallelDownloader) DownloadParts(key, domain string, hashes []Hash) (*PartsDownloadResponse, error) {
	// Acquire semaphore
	p.sem <- struct{}{}
	defer func() { <-p.sem }()

	// Select client in round-robin fashion
	clientIdx := int(p.clientIdx.Add(1)-1) % len(p.clients)
	client := p.clients[clientIdx]

	payload, err := encodeRequest(p.encMode, &PartsDownloadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		Hashes: hashes,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", p.baseURL+partsDownloadEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor+zstd")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", partsDownloadEndpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, partsDownloadEndpoint)
	}

	var r PartsDownloadResponse
	if err = decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

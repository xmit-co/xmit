package protocol

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/xmit-co/xmit/progress"
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

type Client struct {
	Url     string
	client  *http.Client
	EncMode cbor.EncMode
}

func NewClient() *Client {
	url := os.Getenv("XMIT_URL")
	if url == "" {
		url = "https://xmit.co"
	}
	encMode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return &Client{
		Url:     url,
		client:  &http.Client{},
		EncMode: encMode,
	}
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
	ID     string `cbor:"1,keyasint,omitempty"`
	Name   string `cbor:"2,keyasint,omitempty"`
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

// encodeRequest encodes a request to CBOR and compresses it with zstd
func (c *Client) encodeRequest(req interface{}) ([]byte, error) {
	var b bytes.Buffer
	bf := bufio.NewWriter(&b)
	z, err := zstd.NewWriter(bf, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd writer: %w", err)
	}

	e := c.EncMode.NewEncoder(z)
	if err = e.Encode(req); err != nil {
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
func (c *Client) decodeResponse(body io.ReadCloser, resp interface{}) error {
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

// post sends a POST request to the specified endpoint with the given payload
func (c *Client) post(endpoint string, payload []byte, useProgress bool, progressMsg string) (*http.Response, error) {
	var reader io.Reader
	if useProgress {
		reader = progress.NewReader(payload, progressMsg)
	} else {
		reader = bytes.NewReader(payload)
	}

	resp, err := c.client.Post(c.Url+endpoint, "application/cbor+zstd", reader)
	if err != nil {
		return nil, fmt.Errorf("failed to post to %s: %w", endpoint, err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, endpoint)
	}

	return resp, nil
}

func (c *Client) SuggestBundle(key, domain string, id Hash) (*BundleSuggestResponse, error) {
	payload, err := c.encodeRequest(&BundleSuggestRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		ID: id,
	})
	if err != nil {
		return nil, err
	}

	log.Print("🤔 Suggesting bundle…")
	resp, err := c.post(bundleSuggestEndpoint, payload, false, "")
	if err != nil {
		return nil, err
	}

	var r BundleSuggestResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) UploadBundle(key, domain string, bundle []byte) (*BundleUploadResponse, error) {
	payload, err := c.encodeRequest(&BundleUploadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		Bundle: bundle,
	})
	if err != nil {
		return nil, err
	}

	log.Printf("🚶 Uploading bundle (%d bytes)…", len(payload))
	resp, err := c.post(bundleUploadEndpoint, payload, true, "🧘 Bundle upload complete, waiting for server…")
	if err != nil {
		return nil, err
	}

	var r BundleUploadResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) UploadMissing(key string, domain string, i, count int, parts [][]byte) (*MissingUploadResponse, error) {
	payload, err := c.encodeRequest(&MissingUploadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		Parts: parts,
	})
	if err != nil {
		return nil, err
	}

	if len(parts) == 1 {
		log.Printf("🏃 Uploading chunk %d/%d of 1 missing part (%d bytes compressed)…", i+1, count, len(payload))
	} else {
		log.Printf("🏃 Uploading chunk %d/%d of %d missing parts (%d bytes compressed)…", i+1, count, len(parts), len(payload))
	}

	resp, err := c.post(missingUploadEndpoint, payload, true, "🧘 Upload complete, waiting for server…")
	if err != nil {
		return nil, err
	}

	var r MissingUploadResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Finalize(key string, domain string, id Hash) (*FinalizeUploadResponse, error) {
	payload, err := c.encodeRequest(&FinalizeUploadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		ID: id,
	})
	if err != nil {
		return nil, err
	}

	log.Print("🏁 Finalizing…")
	resp, err := c.post(finalizeUploadEndpoint, payload, false, "")
	if err != nil {
		return nil, err
	}

	var r FinalizeUploadResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) DownloadBundle(key, domain, id string) (*BundleDownloadResponse, error) {
	payload, err := c.encodeRequest(&BundleDownloadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		ID: id,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.post(bundleDownloadEndpoint, payload, false, "")
	if err != nil {
		return nil, err
	}

	var r BundleDownloadResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) DownloadParts(key, domain string, hashes []Hash) (*PartsDownloadResponse, error) {
	payload, err := c.encodeRequest(&PartsDownloadRequest{
		Request: Request{
			Key:    key,
			Domain: domain,
		},
		Hashes: hashes,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.post(partsDownloadEndpoint, payload, false, "")
	if err != nil {
		return nil, err
	}

	var r PartsDownloadResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) ListTeams(key string) (*ListTeamsResponse, error) {
	payload, err := c.encodeRequest(&Request{
		Key: key,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.post(listTeamsEndpoint, payload, false, "")
	if err != nil {
		return nil, err
	}

	var r ListTeamsResponse
	if err = c.decodeResponse(resp.Body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

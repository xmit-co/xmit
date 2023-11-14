package protocol

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
	"log"
	"net/http"
	"os"
)

const (
	version                = "0"
	endpointPrefix         = "/api/cli/" + version
	bundleSuggestEndpoint  = endpointPrefix + "/suggest"
	bundleUploadEndpoint   = endpointPrefix + "/bundle"
	missingUploadEndpoint  = endpointPrefix + "/missing"
	finalizeUploadEndpoint = endpointPrefix + "/finalize"
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
	Key string `cbor:"1,keyasint"`
}

type Response struct {
	Success  bool     `cbor:"1,keyasint"`
	Errors   []string `cbor:"2,keyasint,omitempty"`
	Warnings []string `cbor:"3,keyasint,omitempty"`
}

type BundleSuggestRequest struct {
	Request
	Domain string `cbor:"5,keyasint,omitempty"`
	ID     Hash   `cbor:"6,keyasint,omitempty"`
}

type BundleSuggestResponse struct {
	Response
	Present bool   `cbor:"5,keyasint,omitempty"`
	Missing []Hash `cbor:"6,keyasint,omitempty"`
}

type BundleUploadRequest struct {
	Request
	Domain string `cbor:"5,keyasint,omitempty"`
	Bundle []byte `cbor:"6,keyasint,omitempty"`
}

type BundleUploadResponse struct {
	Response
	ID      Hash   `cbor:"5,keyasint,omitempty"`
	Missing []Hash `cbor:"6,keyasint,omitempty"`
}

type MissingUploadRequest struct {
	Request
	Domain string          `cbor:"5,keyasint,omitempty"`
	ID     Hash            `cbor:"6,keyasint,omitempty"`
	Parts  map[Hash][]byte `cbor:"7,keyasint,omitempty"`
}

type MissingUploadResponse struct {
	Response
}

type FinalizeUploadRequest struct {
	Request
	Domain string `cbor:"5,keyasint,omitempty"`
	ID     Hash   `cbor:"6,keyasint,omitempty"`
}

type FinalizeUploadResponse struct {
	Response
	URL string `cbor:"5,keyasint,omitempty"`
}

func (c *Client) SuggestBundle(key, domain string, id Hash) (*BundleSuggestResponse, error) {
	var b bytes.Buffer
	bf := bufio.NewWriter(&b)
	z, err := zstd.NewWriter(bf)
	if err != nil {
		return nil, err
	}
	e := c.EncMode.NewEncoder(z)
	if err = e.Encode(&BundleSuggestRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		ID:     id,
	}); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if err = bf.Flush(); err != nil {
		return nil, err
	}
	log.Printf("🤔 Suggesting bundle by hash (%d bytes)…", b.Len())
	resp, err := c.client.Post(c.Url+bundleSuggestEndpoint, "application/cbor+zstd", bytes.NewReader(b.Bytes()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var r BundleSuggestResponse
	zd, err := zstd.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer zd.Close()
	if err = cbor.NewDecoder(zd).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) UploadBundle(key, domain string, bundle []byte) (*BundleUploadResponse, error) {
	var b bytes.Buffer
	bf := bufio.NewWriter(&b)
	z, err := zstd.NewWriter(bf)
	if err != nil {
		return nil, err
	}
	e := c.EncMode.NewEncoder(z)
	if err = e.Encode(&BundleUploadRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		Bundle: bundle,
	}); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if err = bf.Flush(); err != nil {
		return nil, err
	}
	log.Printf("🚶 Uploading bundle (%d bytes)…", b.Len())
	resp, err := c.client.Post(c.Url+bundleUploadEndpoint, "application/cbor+zstd", bytes.NewReader(b.Bytes()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var r BundleUploadResponse
	zd, err := zstd.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer zd.Close()
	if err = cbor.NewDecoder(zd).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) UploadMissing(key string, domain string, parts map[Hash][]byte) (*MissingUploadResponse, error) {
	var b bytes.Buffer
	bf := bufio.NewWriter(&b)
	z, err := zstd.NewWriter(bf)
	if err != nil {
		return nil, err
	}
	e := c.EncMode.NewEncoder(z)
	if err = e.Encode(&MissingUploadRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		Parts:  parts,
	}); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if err = bf.Flush(); err != nil {
		return nil, err
	}
	log.Printf("🏃 Uploading %d missing parts (%d bytes)…", len(parts), b.Len())
	resp, err := c.client.Post(c.Url+missingUploadEndpoint, "application/cbor+zstd", bytes.NewReader(b.Bytes()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var r MissingUploadResponse
	zd, err := zstd.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer zd.Close()
	if err = cbor.NewDecoder(zd).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Finalize(key string, domain string, id Hash) (*FinalizeUploadResponse, error) {
	var b bytes.Buffer
	bf := bufio.NewWriter(&b)
	z, err := zstd.NewWriter(bf)
	if err != nil {
		return nil, err
	}
	e := c.EncMode.NewEncoder(z)
	if err = e.Encode(&FinalizeUploadRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		ID:     id,
	}); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if err = bf.Flush(); err != nil {
		return nil, err
	}
	log.Printf("🏁 Finalizing (%v bytes)…", b.Len())
	resp, err := c.client.Post(c.Url+finalizeUploadEndpoint, "application/cbor+zstd", bytes.NewReader(b.Bytes()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var r FinalizeUploadResponse
	zd, err := zstd.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer zd.Close()
	if err = cbor.NewDecoder(zd).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

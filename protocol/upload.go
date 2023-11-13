package protocol

import (
	"bytes"
	"fmt"
	"github.com/fxamacker/cbor/v2"
	"net/http"
)

const (
	VERSION                  = "0.0.1"
	BACKEND_URL              = "https://xmit.co"
	BUNDLE_UPLOAD_ENDPOINT   = BACKEND_URL + "/bundle"
	MISSING_UPLOAD_ENDPOINT  = BACKEND_URL + "/missing"
	FINALIZE_UPLOAD_ENDPOINT = BACKEND_URL + "/finalize"
)

var (
	client = &http.Client{}
)

type Request struct {
	Key string `cbor:"1,keyasint"`
}

type Response struct {
	Success  bool     `cbor:"1,keyasint"`
	Errors   []string `cbor:"2,keyasint,omitempty"`
	Warnings []string `cbor:"3,keyasint,omitempty"`
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

func UploadBundle(key, domain string, bundle []byte) (*BundleUploadResponse, error) {
	b, err := cbor.Marshal(&BundleUploadRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		Bundle: bundle,
	})
	if err != nil {
		return nil, err
	}
	resp, err := client.Post(BUNDLE_UPLOAD_ENDPOINT, "application/cbor", bytes.NewReader(b))
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
	err = cbor.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func UploadMissing(key string, domain string, parts map[Hash][]byte) (*MissingUploadResponse, error) {
	b, err := cbor.Marshal(&MissingUploadRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		Parts:  parts,
	})
	if err != nil {
		return nil, err
	}
	resp, err := client.Post(MISSING_UPLOAD_ENDPOINT, "application/cbor", bytes.NewReader(b))
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
	err = cbor.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func Finalize(key string, domain string, id Hash) (*FinalizeUploadResponse, error) {
	b, err := cbor.Marshal(&FinalizeUploadRequest{
		Request: Request{
			Key: key,
		},
		Domain: domain,
		ID:     id,
	})
	if err != nil {
		return nil, err
	}
	resp, err := client.Post(FINALIZE_UPLOAD_ENDPOINT, "application/cbor", bytes.NewReader(b))
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
	err = cbor.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

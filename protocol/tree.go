package protocol

type Hash [32]byte

type Node struct {
	Children map[string]*Node `cbor:"1,keyasint,omitempty"`
	Hash     Hash             `cbor:"2,keyasint,omitempty"`
}

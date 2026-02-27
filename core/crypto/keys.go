package crypto

// KeyPair describes an identity key pair for anonymous accounts.
type KeyPair struct {
	PublicKey  []byte
	PrivateKey []byte
}

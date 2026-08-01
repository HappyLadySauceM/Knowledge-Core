// Command authkeys generates a matching Ed25519 key pair for local Identity
// and Gateway development. Private-key output must be stored as a secret.
package main

import (
	"fmt"
	"log"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
)

func main() {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("IDENTITY_AUTH_PRIVATE_KEY=%s\n", keys.PrivateKey)
	fmt.Printf("IDENTITY_AUTH_PUBLIC_KEY=%s\n", keys.PublicKey)
	fmt.Printf("GATEWAY_AUTH_PUBLIC_KEY=%s\n", keys.PublicKey)
}

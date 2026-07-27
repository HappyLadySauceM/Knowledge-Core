package main

import (
	"fmt"
	"os"

	foundationauth "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/auth"
)

func main() {
	keyPair, err := foundationauth.GenerateKeyPair()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("KC_IDENTITY_JWT_PRIVATE_KEY=%s\n", keyPair.PrivateKey)
	fmt.Printf("KC_GATEWAY_JWT_PUBLIC_KEY=%s\n", keyPair.PublicKey)
}

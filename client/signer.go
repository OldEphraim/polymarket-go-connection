package client

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

func SignOrder(order *Order, privateKey *ecdsa.PrivateKey) error {
	// Polymarket's EIP-712 domain
	domain := apitypes.TypedDataDomain{
		Name:              "Polymarket CTF Exchange",
		Version:           "1",
		ChainId:           math.NewHexOrDecimal256(137),
		VerifyingContract: "0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E",
	}

	types := apitypes.Types{
		"EIP712Domain": []apitypes.Type{
			{Name: "name", Type: "string"},
			{Name: "version", Type: "string"},
			{Name: "chainId", Type: "uint256"},
			{Name: "verifyingContract", Type: "address"},
		},
		"Order": []apitypes.Type{
			{Name: "salt", Type: "uint256"},
			{Name: "maker", Type: "address"},
			{Name: "signer", Type: "address"},
			{Name: "taker", Type: "address"},
			{Name: "tokenId", Type: "uint256"},
			{Name: "makerAmount", Type: "uint256"},
			{Name: "takerAmount", Type: "uint256"},
			{Name: "expiration", Type: "uint256"},
			{Name: "nonce", Type: "uint256"},
			{Name: "feeRateBps", Type: "uint256"},
			{Name: "side", Type: "uint8"},
			{Name: "signatureType", Type: "uint8"},
		},
	}

	// Convert side to *big.Int for uint8
	sideInt := big.NewInt(0)
	if order.Side == "sell" {
		sideInt = big.NewInt(1)
	}

	// Convert signatureType to *big.Int
	sigTypeInt := big.NewInt(int64(order.SignatureType))

	// The EIP-712 library needs *big.Int for numeric types
	message := apitypes.TypedDataMessage{
		"salt":          order.Salt,
		"maker":         order.Maker,
		"signer":        order.Signer,
		"taker":         order.Taker,
		"tokenId":       order.TokenID,
		"makerAmount":   order.MakerAmount,
		"takerAmount":   order.TakerAmount,
		"expiration":    order.Expiration,
		"nonce":         order.Nonce,
		"feeRateBps":    order.FeeRateBps,
		"side":          sideInt,    // *big.Int for uint8
		"signatureType": sigTypeInt, // *big.Int for uint8
	}

	typedData := apitypes.TypedData{
		Types:       types,
		PrimaryType: "Order",
		Domain:      domain,
		Message:     message,
	}

	// Create the hash to sign
	hash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return fmt.Errorf("failed to hash message: %w", err)
	}

	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return fmt.Errorf("failed to hash domain: %w", err)
	}

	// Construct the final digest according to EIP-712
	digest := crypto.Keccak256(
		[]byte{0x19, 0x01},
		domainSeparator,
		hash,
	)

	// Sign the digest
	signature, err := crypto.Sign(digest, privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign: %w", err)
	}

	// Adjust V value for Ethereum signatures (27 or 28)
	signature[64] += 27

	// Set the signature on the order
	order.Signature = "0x" + hex.EncodeToString(signature)

	return nil
}

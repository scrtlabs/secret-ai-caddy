package querycontract

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/miscreant/miscreant.go"
	"go.uber.org/zap"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

func getSecretNode() string {
	if node := os.Getenv("SECRET_NODE"); node != "" {
		return node
	}
	return "pulsar.lcd.secretnodes.com"
}

func getHTTPClient() *http.Client {
	skipSSL := os.Getenv("SKIP_SSL_VALIDATION")
	if strings.ToLower(skipSSL) == "true" {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		return &http.Client{Transport: tr}
	}
	return http.DefaultClient
}

func Trace() string {
	pc := make([]uintptr, 10) // at least 1 entry needed
	runtime.Callers(2, pc)
	f := runtime.FuncForPC(pc[0])
	file, line := f.FileLine(pc[0])
	return fmt.Sprintf("<<<<< %s:%d %s >>>>>", file, line, f.Name())
}

// HKDF salt used in key derivation
var hkdfSalt = []byte{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x02, 0x4b, 0xea, 0xd8, 0xdf, 0x69, 0x99,
	0x08, 0x52, 0xc2, 0x02, 0xdb, 0x0e, 0x00, 0x97,
	0xc1, 0xa1, 0x2e, 0xa6, 0x37, 0xd7, 0xe9, 0x6d,
}

type WASMContext struct {
	cliContext      map[string]string
	testKeyPairPath string
	nonce           []byte
}

func NewWASMContext(cliContext map[string]string) (*WASMContext, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New(fmt.Sprintf("failed to generate nonce: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to generate nonce: %w", err)
	}

	return &WASMContext{
		cliContext: cliContext,
		nonce:      nonce,
	}, nil
}

func (w *WASMContext) getTxSenderKeyPair() ([]byte, []byte, error) {
	keyPairFilePath := w.testKeyPairPath
	if keyPairFilePath == "" {
		keyPairFilePath = filepath.Join(w.cliContext["home_dir"], "id_tx_io.json")
	}

	if _, err := os.Stat(keyPairFilePath); os.IsNotExist(err) {
		privKey := make([]byte, 32)
		if _, err := rand.Read(privKey); err != nil {
			return nil, nil, errors.New(fmt.Sprintf("failed to generate private key: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to generate private key: %w", err)
		}

		var pubKey [32]byte
		curve25519.ScalarBaseMult(&pubKey, (*[32]byte)(privKey))

		keyPair := map[string]string{
			"private": hex.EncodeToString(privKey),
			"public":  hex.EncodeToString(pubKey[:]),
		}

		jsonData, err := json.Marshal(keyPair)
		if err != nil {
			return nil, nil, errors.New(fmt.Sprintf("failed to marshal key pair: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to marshal key pair: %w", err)
		}

		if err := ioutil.WriteFile(keyPairFilePath, jsonData, 0600); err != nil {
			return nil, nil, errors.New(fmt.Sprintf("failed to write key pair file: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to write key pair file: %w", err)
		}

		return privKey, pubKey[:], nil
	}

	data, err := ioutil.ReadFile(keyPairFilePath)
	if err != nil {
		return nil, nil, errors.New(fmt.Sprintf("failed to read key pair file: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to read key pair file: %w", err)
	}

	var keyPair map[string]string
	if err := json.Unmarshal(data, &keyPair); err != nil {
		return nil, nil, errors.New(fmt.Sprintf("failed to unmarshal key pair: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to unmarshal key pair: %w", err)
	}

	privKey, err := hex.DecodeString(keyPair["private"])
	if err != nil {
		return nil, nil, errors.New(fmt.Sprintf("failed to decode private key: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode private key: %w", err)
	}

	pubKey, err := hex.DecodeString(keyPair["public"])
	if err != nil {
		return nil, nil, errors.New(fmt.Sprintf("failed to decode public key: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode public key: %w", err)
	}

	return privKey, pubKey, nil
}

func (w *WASMContext) getConsensusIOPubKey() ([]byte, error) {
	client := getHTTPClient()
	resp, err := client.Get(fmt.Sprintf("https://%s/registration/v1beta1/tx-key", getSecretNode()))
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to fetch consensus IO public key: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to fetch consensus IO public key: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.New(fmt.Sprintf("failed to decode consensus IO response: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode consensus IO response: %w", err)
	}

	key, err := base64.StdEncoding.DecodeString(result["key"])
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to decode consensus IO key: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode consensus IO key: %w", err)
	}

	return key, nil
}

func (w *WASMContext) getTxEncryptionKey(txSenderPrivKey []byte) ([]byte, error) {
	consensusIOPubKey, err := w.getConsensusIOPubKey()
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to get consensus IO public key: %s", err) + "\n" + Trace()) //err
	}

	var shared [32]byte
	curve25519.ScalarMult(&shared, (*[32]byte)(txSenderPrivKey), (*[32]byte)(consensusIOPubKey))

	hash := sha256.New
	hkdf := hkdf.New(hash, append(shared[:], w.nonce...), hkdfSalt, nil)

	key := make([]byte, 32)
	if _, err := hkdf.Read(key); err != nil {
		return nil, errors.New(fmt.Sprintf("failed to derive encryption key: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to derive encryption key: %w", err)
	}

	return key, nil
}

func (w *WASMContext) Encrypt(plaintext []byte) ([]byte, error) {
	txSenderPrivKey, txSenderPubKey, err := w.getTxSenderKeyPair()
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to get tx sender key pair: %s", err) + "\n" + Trace()) //err
	}

	txEncryptionKey, err := w.getTxEncryptionKey(txSenderPrivKey)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to get tx encryption key: %s", err) + "\n" + Trace()) // err
	}

	siv, err := miscreant.NewAESCMACSIV(txEncryptionKey)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to create AES-SIV: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to create AES-SIV: %w", err)
	}

	ciphertext := make([]byte, 0)
	ciphertext, err = siv.Seal(ciphertext, plaintext, []byte{})
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to encrypt: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to encrypt: %w", err)
	}

	result := append(w.nonce, txSenderPubKey...)
	result = append(result, ciphertext...)

	return result, nil
}

func (w *WASMContext) Decrypt(ciphertext []byte) ([]byte, error) {
	txSenderPrivKey, _, err := w.getTxSenderKeyPair()
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to get tx sender key pair: %s", err) + "\n" + Trace()) //err
	}

	txEncryptionKey, err := w.getTxEncryptionKey(txSenderPrivKey)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to get tx encryption key: %s", err) + "\n" + Trace()) //err
	}

	siv, err := miscreant.NewAESCMACSIV(txEncryptionKey)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to create AES-SIV: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to create AES-SIV: %w", err)
	}

	plaintext := make([]byte, 0)
	plaintext, err = siv.Open(plaintext, ciphertext, []byte{})
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to decrypt: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// getMapKeys extracts keys from a map for logging purposes
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func fetchCodeHash(contractAddress string) (string, error) {
	// API endpoint to get the code hash by contract address
	url := fmt.Sprintf("https://%s/compute/v1beta1/code_hash/by_contract_address/%s", getSecretNode(), contractAddress)

	client := getHTTPClient()
	resp, err := client.Get(url)
	if err != nil {
		return "", errors.New(fmt.Sprintf("failed to fetch code hash: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to fetch code hash: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New(fmt.Sprintf("unexpected response status: %s", resp.Status) + "\n" + Trace()) //fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	var result struct {
		CodeHash string `json:"code_hash"`
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return "", errors.New(fmt.Sprintf("failed to decode code hash response: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode code hash response: %w", err)
	}

	return result.CodeHash, nil
}

func QueryContract(contractAddress string, query map[string]interface{}) (map[string]interface{}, error) {
	// Fetch the code hash dynamically
	codeHash, err := fetchCodeHash(contractAddress)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to fetch code hash: %s", err) + "\n" + Trace()) // err
	}

	cliContext := map[string]string{"home_dir": ""}
	context, err := NewWASMContext(cliContext)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to create WASM context: %s", err) + "\n" + Trace()) // err
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to marshal query: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to marshal query: %w", err)
	}

	textQuery := codeHash + string(queryJSON)
	encryptedData, err := context.Encrypt([]byte(textQuery))
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to encrypt query: %s", err) + "\n" + Trace()) //err
	}

	encodedData := base64.URLEncoding.EncodeToString(encryptedData)
	url := fmt.Sprintf("https://%s/compute/v1beta1/query/%s?query=%s", getSecretNode(), contractAddress, encodedData)

	client := getHTTPClient()
	resp, err := client.Get(url)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to query contract: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to query contract: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to read response body: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to read response body: %w", err)
	}

	var responseData map[string]interface{}
	if err := json.Unmarshal(responseBody, &responseData); err != nil {
		return nil, errors.New(fmt.Sprintf("failed to unmarshal response: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if encodedResponse, ok := responseData["data"].(string); ok {
		decodedResponse, err := base64.StdEncoding.DecodeString(encodedResponse)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("failed to decode response data: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode response data: %w", err)
		}

		decryptedData, err := context.Decrypt(decodedResponse)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("failed to decrypt response data: %s", err) + "\n" + Trace()) //err
		}

		decodedData, err := base64.StdEncoding.DecodeString(string(decryptedData))
		if err != nil {
			return nil, errors.New(fmt.Sprintf("failed to decode decrypted data: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to decode decrypted data: %w", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(decodedData, &result); err != nil {
			return nil, errors.New(fmt.Sprintf("failed to parse decrypted data: %s", err) + "\n" + Trace()) //fmt.Errorf("failed to parse decrypted data: %w", err)
		}

		// Log count of entries instead of full JSON
		logger := caddy.Log()
		if apiKeys, ok := result["api_keys"].([]interface{}); ok {
			logger.Info("Query result: Retrieved API key entries", zap.Int("count", len(apiKeys)))
		} else {
			logger.Info("Query result: Response received", zap.Strings("structure_keys", getMapKeys(result)))
		}

		return result, nil
	}

	return nil, errors.New("response data not found" + "\n" + Trace()) //fmt.Errorf("response data not found")
}

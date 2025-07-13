package secret_reverse_proxy

// MockContractQuerier implements contract querying for testing
type MockContractQuerier struct {
	ValidHashes map[string]bool
	ShouldFail  bool
	FailError   error
}

// QueryContract mocks the contract query functionality
func (m *MockContractQuerier) QueryContract(contractAddress string, query map[string]any) (map[string]any, error) {
	if m.ShouldFail {
		return nil, m.FailError
	}
	
	// Return mock API keys based on what we want to test
	apiKeys := make([]any, 0)
	for hash := range m.ValidHashes {
		apiKeys = append(apiKeys, map[string]any{
			"hashed_key": hash,
		})
	}
	
	return map[string]any{
		"api_keys": apiKeys,
	}, nil
}
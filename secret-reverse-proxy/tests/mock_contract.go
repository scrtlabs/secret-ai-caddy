package tests

// MockQueryContract replaces the real contract query functionality for testing
type MockQueryContract struct {
	ValidHashes map[string]bool
	ShouldFail  bool
	FailError   error
}

var mockContract *MockQueryContract

// Mock the querycontract.QueryContract function
func MockQueryContractFunc(contractAddress string, query map[string]any) (map[string]any, error) {
	if mockContract.ShouldFail {
		return nil, mockContract.FailError
	}

	// Return mock API keys based on what we want to test
	apiKeys := make([]any, 0)
	for hash := range mockContract.ValidHashes {
		apiKeys = append(apiKeys, map[string]any{
			"hashed_key": hash,
		})
	}

	return map[string]any{
		"api_keys": apiKeys,
	}, nil
}

// Helper function to set up mock contract for testing
func SetupMockContract(validHashes map[string]bool) {
	mockContract = &MockQueryContract{
		ValidHashes: validHashes,
		ShouldFail:  false,
	}
}

// Helper function to set up failing mock contract
func SetupFailingMockContract(err error) {
	mockContract = &MockQueryContract{
		ShouldFail: true,
		FailError:  err,
	}
}
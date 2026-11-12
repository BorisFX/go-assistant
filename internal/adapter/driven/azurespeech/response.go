package azurespeech

import (
	"encoding/json"
	"fmt"
)

type response struct {
	RecognitionStatus string `json:"RecognitionStatus"`
	DisplayText       string `json:"DisplayText"`
}

func displayText(body []byte) (string, error) {
	var result response
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse Azure Speech response: %w", err)
	}
	return result.DisplayText, nil
}

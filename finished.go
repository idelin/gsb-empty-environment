package tls13

import "crypto/hmac"

// FinishedMAC computes HMAC(finishedKey, transcriptHash) when finishedKey is
// already the expanded "finished" key.
func FinishedMAC(suite CipherSuite, finishedKey, transcriptHash []byte) []byte {
	mac := hmac.New(suite.hashFunc(), finishedKey)
	mac.Write(transcriptHash)
	return mac.Sum(nil)
}

// CalculateVerifyData derives the "finished" key from trafficSecret and then
// computes Finished.verify_data over transcriptHash.
func CalculateVerifyData(suite CipherSuite, trafficSecret, transcriptHash []byte) []byte {
	return FinishedMAC(suite, FinishedKey(suite, trafficSecret), transcriptHash)
}

// VerifyData is an alias for CalculateVerifyData.
func VerifyData(suite CipherSuite, trafficSecret, transcriptHash []byte) []byte {
	return CalculateVerifyData(suite, trafficSecret, transcriptHash)
}

// ComputeBinder computes a PSK binder HMAC from an already-derived binder_key
// and Transcript-Hash(Truncate(ClientHello)).
func ComputeBinder(suite CipherSuite, binderKey, truncatedClientHelloHash []byte) []byte {
	return FinishedMAC(suite, FinishedKey(suite, binderKey), truncatedClientHelloHash)
}

// VerifyBinder checks a PSK binder in constant time.
func VerifyBinder(suite CipherSuite, binderKey, truncatedClientHelloHash, binder []byte) bool {
	return hmac.Equal(ComputeBinder(suite, binderKey, truncatedClientHelloHash), binder)
}

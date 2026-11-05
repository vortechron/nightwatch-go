package omnirules

// DefaultLimits are the limits of Omni's config/omni.php: 10 MB compressed,
// 64 MB decompressed, 10,000 records per batch.
var DefaultLimits = DecoderLimits{
	MaxBodyBytes:         10 << 20,
	MaxDecompressedBytes: 64 << 20,
	MaxRecords:           10000,
}

// Validate runs the decoder rules and then the storage rules over a body.
func Validate(body []byte, contentEncoding string) (Batch, *PayloadError) {
	batch, err := NewDecoder(DefaultLimits).Decode(body, contentEncoding)
	if err != nil {
		if payloadErr, ok := err.(*PayloadError); ok { //nolint:errorlint // the decoder only returns *PayloadError
			return Batch{}, payloadErr
		}

		return Batch{}, newPayloadError(err.Error(), 400)
	}

	for index, event := range batch.Events {
		if storageErr := CheckStorage(event, index); storageErr != nil {
			return Batch{}, storageErr
		}
	}

	return batch, nil
}

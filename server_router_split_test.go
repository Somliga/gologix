package gologix

import "testing"

// readOnlyTags is what a simulator wants to be: a tag store with no write path at all. It must be
// possible to declare such a type, satisfying TagProvider, without also being forced to implement
// a write method. Before the split it could not be — CIPEndpoint required IOWrite, so every tag
// provider carried a write method it did not want, and a consumer whose first rule is "never write
// to a PLC" had to accept one anyway.
type readOnlyTags struct{}

func (readOnlyTags) TagRead(tag string, qty int16) (any, error) { return int32(0), nil }

func TestATagProviderNeedsNoWriteMethod(t *testing.T) {
	var _ TagProvider = readOnlyTags{}
}

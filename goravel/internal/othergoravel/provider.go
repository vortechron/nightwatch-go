// Package goravel is a stand-in for any other package named "goravel" that
// exports a ServiceProvider (reverb-go has one). Goravel keys providers by
// their %T type name, so two such types would be the same provider.
package goravel

import "github.com/goravel/framework/contracts/foundation"

// ServiceProvider records whether Goravel registered it.
type ServiceProvider struct {
	Registered *bool
}

func (p ServiceProvider) Register(foundation.Application) { *p.Registered = true }

func (ServiceProvider) Boot(foundation.Application) {}

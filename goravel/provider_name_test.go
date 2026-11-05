package goravel

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"
	mocksfoundation "github.com/goravel/framework/mocks/foundation"

	nightwatch "github.com/vortechron/nightwatch-go"
	other "github.com/vortechron/nightwatch-go/goravel/internal/othergoravel"
)

// Goravel keys providers by fmt.Sprintf("%T", provider). A type named
// goravel.ServiceProvider would collide with every other package called goravel
// that exports ServiceProvider, and the second one is silently skipped.
func TestProviderTypeNameDoesNotCollideWithOtherGoravelPackages(t *testing.T) {
	if mine, theirs := fmt.Sprintf("%T", &NightwatchServiceProvider{}), fmt.Sprintf("%T", &other.ServiceProvider{}); mine == theirs {
		t.Fatalf("both providers are named %s", mine)
	}

	registered := false
	repo := foundation.NewProviderRepository()
	repo.Add([]contractsfoundation.ServiceProvider{
		&other.ServiceProvider{Registered: &registered},
		&NightwatchServiceProvider{Config: &nightwatch.Config{}},
	})

	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Singleton(BindingClient, mock.Anything).Once()
	app.EXPECT().Make(BindingClient).Return(nightwatch.New(nightwatch.Config{}), nil).Maybe()

	got := repo.Register(app)
	if len(got) != 2 {
		t.Fatalf("both providers must be registered, got %d", len(got))
	}
	if !registered {
		t.Fatal("the other goravel.ServiceProvider was skipped")
	}
}

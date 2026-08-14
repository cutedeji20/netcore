package payments

import (
	"context"
	"errors"
)

// DisabledGateway is the safe runtime default until an operator configures a
// gateway through the project's secret-management path. It creates no pending
// payments and never pretends to have verified one.
type DisabledGateway struct{}

func NewDisabledGateway() DisabledGateway { return DisabledGateway{} }
func (DisabledGateway) Name() string      { return "disabled" }
func (DisabledGateway) Available() bool   { return false }
func (DisabledGateway) Initialize(context.Context, GatewayInitialization) (GatewayCheckout, error) {
	return GatewayCheckout{}, errors.New("payment gateway is not configured")
}
func (DisabledGateway) Verify(context.Context, string) (GatewayVerification, error) {
	return GatewayVerification{}, errors.New("payment gateway is not configured")
}

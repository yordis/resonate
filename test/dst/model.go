package dst

import (
	"fmt"

	"github.com/resonatehq/resonate/internal/kernel/types"
	"github.com/resonatehq/resonate/pkg/promise"
	"github.com/resonatehq/resonate/pkg/subscription"
)

type Model struct {
	promises  Promises
	responses map[types.APIKind]ResponseValidator
}

func NewModel() *Model {
	return &Model{
		promises:  map[string]*PromiseModel{},
		responses: map[types.APIKind]ResponseValidator{},
	}
}

func (m *Model) AddResponse(kind types.APIKind, response ResponseValidator) {
	m.responses[kind] = response
}

func (m *Model) Step(req *types.Request, res *types.Response, err error) error {
	if err != nil {
		switch err.Error() {
		case "api submission queue full":
			return nil
		case "subsystem:store:sqlite submission queue full":
			return nil
		case "subsystem:network:dst submission queue full":
			return nil
		default:
			return fmt.Errorf("unexpected error '%v'", err)
		}
	}

	if req.Kind != res.Kind {
		return fmt.Errorf("unexpected response kind '%s' for request kind '%s'", res.Kind, req.Kind)
	}

	if f, ok := m.responses[req.Kind]; ok {
		return f(req, res)
	}

	return fmt.Errorf("unexpected request/response kind '%s'", req.Kind)
}

func (m *Model) ValidateReadPromise(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.ReadPromise.Id)
	return pm.readPromise(req.ReadPromise, res.ReadPromise)
}

func (m *Model) ValidateSearchPromises(req *types.Request, res *types.Response) error {
	for _, p := range res.SearchPromises.Promises {
		pm := m.promises.Get(p.Id)
		if err := pm.searchPromise(req.SearchPromises, res.SearchPromises, p); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) ValidatCreatePromise(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.CreatePromise.Id)
	return pm.createPromise(req.CreatePromise, res.CreatePromise)
}

func (m *Model) ValidateCancelPromise(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.CancelPromise.Id)
	return pm.cancelPromise(req.CancelPromise, res.CancelPromise)
}

func (m *Model) ValidateResolvePromise(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.ResolvePromise.Id)
	return pm.resolvePromise(req.ResolvePromise, res.ResolvePromise)
}

func (m *Model) ValidateRejectPromise(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.RejectPromise.Id)
	return pm.rejectPromise(req.RejectPromise, res.RejectPromise)
}

func (m *Model) ValidateReadSubscriptions(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.ReadSubscriptions.PromiseId)
	return pm.readSubscriptions(req.ReadSubscriptions, res.ReadSubscriptions)
}

func (m *Model) ValidateCreateSubscription(req *types.Request, res *types.Response) error {
	pm := m.promises.Get(req.CreateSubscription.PromiseId)
	return pm.createSubscription(req.CreateSubscription, res.CreateSubscription)
}

func (m *Model) ValidateDeleteSubscription(req *types.Request, res *types.Response) error {
	// pm := m.promises.Get(req.DeleteSubscription.PromiseId)
	// return pm.deleteSubscription(req.DeleteSubscription, res.DeleteSubscription)
	return nil
}

type Promises map[string]*PromiseModel

func (p Promises) Get(id string) *PromiseModel {
	if _, ok := p[id]; !ok {
		p[id] = &PromiseModel{}
	}

	return p[id]
}

type ResponseValidator func(*types.Request, *types.Response) error

type PromiseModel struct {
	promise       *promise.Promise
	subscriptions []*subscription.Subscription
}

func (m *PromiseModel) readPromise(req *types.ReadPromiseRequest, res *types.ReadPromiseResponse) error {
	switch res.Status {
	case types.ResponseOK:
		if m.completed() && res.Promise.State == promise.Pending {
			return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, res.Promise.State)
		}
	case types.ResponseCreated:
		return fmt.Errorf("invalid response '%d' for read promise request", res.Status)
	case types.ResponseForbidden:
		return fmt.Errorf("invalid response '%d' for read promise request", res.Status)
	case types.ResponseNotFound:
		if m.promise != nil {
			return fmt.Errorf("promise exists %s", m.promise)
		}
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifyPromise(res.Promise)
}

func (m *PromiseModel) searchPromise(req *types.SearchPromisesRequest, res *types.SearchPromisesResponse, p *promise.Promise) error {
	switch res.Status {
	case types.ResponseOK:
		if m.completed() && p.State == promise.Pending {
			return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, p.State)
		}
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifyPromise(p)
}

func (m *PromiseModel) createPromise(req *types.CreatePromiseRequest, res *types.CreatePromiseResponse) error {
	switch res.Status {
	case types.ResponseOK:
		if m.promise != nil {
			if !m.paramIkeyMatch(res.Promise) {
				return fmt.Errorf("ikey mismatch (%s, %s)", m.promise.Param.Ikey, res.Promise.Param.Ikey)
			}
		}
	case types.ResponseCreated:
		if m.promise != nil {
			return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Pending)
		}
	case types.ResponseForbidden:
	case types.ResponseNotFound:
		return fmt.Errorf("invalid response '%d' for create promise request", res.Status)
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifyPromise(res.Promise)
}

func (m *PromiseModel) cancelPromise(req *types.CancelPromiseRequest, res *types.CancelPromiseResponse) error {
	switch res.Status {
	case types.ResponseOK:
		if m.completed() {
			if !m.valueIkeyMatch(res.Promise) {
				return fmt.Errorf("ikey mismatch (%s, %s)", m.promise.Value.Ikey, res.Promise.Value.Ikey)
			} else if m.promise.State != promise.Canceled {
				return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Canceled)
			}
		}
	case types.ResponseCreated:
		if m.completed() {
			return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Canceled)
		}
	case types.ResponseForbidden:
	case types.ResponseNotFound:
		if m.promise != nil {
			return fmt.Errorf("promise exists %s", m.promise)
		}
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifyPromise(res.Promise)
}

func (m *PromiseModel) resolvePromise(req *types.ResolvePromiseRequest, res *types.ResolvePromiseResponse) error {
	switch res.Status {
	case types.ResponseOK:
		if m.completed() {
			if !m.valueIkeyMatch(res.Promise) {
				return fmt.Errorf("ikey mismatch (%s, %s)", m.promise.Value.Ikey, res.Promise.Value.Ikey)
			} else if m.promise.State != promise.Resolved {
				return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Resolved)
			}
		}
	case types.ResponseCreated:
		if m.completed() {
			return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Resolved)
		}
	case types.ResponseForbidden:
	case types.ResponseNotFound:
		if m.promise != nil {
			return fmt.Errorf("promise exists %s", m.promise)
		}
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifyPromise(res.Promise)
}

func (m *PromiseModel) rejectPromise(req *types.RejectPromiseRequest, res *types.RejectPromiseResponse) error {
	switch res.Status {
	case types.ResponseOK:
		if m.completed() {
			if !m.valueIkeyMatch(res.Promise) {
				return fmt.Errorf("ikey mismatch (%s, %s)", m.promise.Value.Ikey, res.Promise.Value.Ikey)
			} else if m.promise.State != promise.Rejected {
				return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Rejected)
			}
		}
	case types.ResponseCreated:
		if m.completed() {
			return fmt.Errorf("invalid state transition (%s -> %s)", m.promise.State, promise.Rejected)
		}
	case types.ResponseForbidden:
	case types.ResponseNotFound:
		if m.promise != nil {
			return fmt.Errorf("promise exists %s", m.promise)
		}
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifyPromise(res.Promise)
}

func (m *PromiseModel) verifyPromise(promise *promise.Promise) error {
	if m.promise != nil && promise != nil {
		if m.promise.Id != promise.Id ||
			m.promise.Timeout != promise.Timeout ||
			(m.promise.Param.Ikey != nil && !m.paramIkeyMatch(promise)) ||
			string(m.promise.Param.Data) != string(promise.Param.Data) ||
			(m.completed() && m.promise.Value.Ikey != nil && !m.valueIkeyMatch(promise)) ||
			(m.completed() && string(m.promise.Value.Data) != string(promise.Value.Data)) {

			return fmt.Errorf("promises do not match (%s, %s)", m.promise, promise)
		}
	}

	// otherwise set model promise to response promise
	m.promise = promise

	return nil
}

func (m *PromiseModel) readSubscriptions(req *types.ReadSubscriptionsRequest, res *types.ReadSubscriptionsResponse) error {
	switch res.Status {
	case types.ResponseOK:
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	for _, subscription := range res.Subscriptions {
		if err := m.verifySubscription(subscription); err != nil {
			return err
		}
	}

	return nil
}

func (m *PromiseModel) createSubscription(req *types.CreateSubscriptionRequest, res *types.CreateSubscriptionResponse) error {
	switch res.Status {
	case types.ResponseCreated:
		for _, subscription := range m.subscriptions {
			if subscription.Url == res.Subscription.Url {
				return fmt.Errorf("subscription '%s' already exists for promise '%s'", res.Subscription.Url, m.promise.Id)
			}
		}
	case types.ResponseConflict:
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	return m.verifySubscription(res.Subscription)
}

func (m *PromiseModel) deleteSubscription(req *types.DeleteSubscriptionRequest, res *types.DeleteSubscriptionResponse) error {
	switch res.Status {
	case types.ResponseNoContent:
	case types.ResponseNotFound:
		for _, subscription := range m.subscriptions {
			if subscription.Id == req.Id {
				return fmt.Errorf("subscription exists '%d' already exists for promise '%s'", subscription.Id, m.promise.Id)
			}
		}
	default:
		return fmt.Errorf("unexpected resonse status '%d'", res.Status)
	}

	for i, subscription := range m.subscriptions {
		if subscription.Id == req.Id {
			m.subscriptions = append(m.subscriptions[:i], m.subscriptions[i+1:]...)
		}
	}

	return nil
}

func (m *PromiseModel) verifySubscription(subscription *subscription.Subscription) error {
	if subscription != nil {
		var found bool
		for _, s := range m.subscriptions {
			if s.Id == subscription.Id {
				if s.Url != subscription.Url {
					return fmt.Errorf("subscriptions do not match (%s, %s)", s, subscription)
				}

				s = subscription
				found = true
			}
		}

		if !found {
			m.subscriptions = append(m.subscriptions, subscription)
		}
	}

	return nil
}

func (m *PromiseModel) paramIkeyMatch(promise *promise.Promise) bool {
	return m.promise.Param.Ikey != nil && promise.Param.Ikey != nil && *m.promise.Param.Ikey == *promise.Param.Ikey
}

func (m *PromiseModel) valueIkeyMatch(promise *promise.Promise) bool {
	return m.promise.Value.Ikey != nil && promise.Value.Ikey != nil && *m.promise.Value.Ikey == *promise.Value.Ikey
}

func (m *PromiseModel) completed() bool {
	return m.promise != nil && m.promise.State != promise.Pending
}

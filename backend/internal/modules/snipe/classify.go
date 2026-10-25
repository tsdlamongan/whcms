package snipe

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// fatalAccountRE spots registrar messages that mean the reseller account (not
// the domain) is the problem: out of balance, wrong deposit state. Retrying
// the same call for every hunt cannot fix those, so they pause the whole
// supervisor instead (the same idea as the original script's fatalPattern).
var fatalAccountRE = regexp.MustCompile(`(?i)balance|saldo|insufficient|not enough|deposit`)

// takenRE spots a "this name is not up for grabs" reply that did not arrive as
// HTTP 409 (some registrars answer 400/422 with such a message). It is a
// definitive refusal of this domain, not a reason to count the hunt as broken.
var takenRE = regexp.MustCompile(`(?i)not available|unavailable|taken|already registered`)

// maxBackoff bounds any registrar-supplied wait: a Retry-After of an hour must
// not park a hunt through its own drop window.
const maxBackoff = 30 * time.Second

// outcomeKind is what a hunt should do after a registrar call failed.
type outcomeKind int

const (
	// outTaken: a definitive "someone else has it" (HTTP 409). Keep trying.
	outTaken outcomeKind = iota
	// outBackoff: rate limited. Wait wait, then keep trying.
	outBackoff
	// outAmbiguous: the registrar may have acted (timeout, 5xx, unreadable
	// reply). Verify against our own account before trying again.
	outAmbiguous
	// outNotSent: the request never left (circuit breaker). Wait, then retry.
	outNotSent
	// outPause: credentials rejected or reseller out of balance. Stop every
	// hunt until the account probe is healthy again; the snipe stays active.
	outPause
	// outFail: a permanent problem with this one snipe (bad customer/profile).
	outFail
	// outRetry: any other definitive rejection. Keep trying, but count it.
	outRetry
)

// outcome is the classified result of one failed registrar call.
type outcome struct {
	kind outcomeKind
	wait time.Duration
	msg  string
	// balance marks an outPause caused by the reseller running out of money
	// (as opposed to rejected credentials): that pause only ends once the
	// balance rises.
	balance bool
}

// classify maps a registrar error onto what the hunt should do next. It leans
// on the wire detail the rdash adapter attaches (ports.RegistrarCallError) and
// only falls back to apperr codes/messages when there is none. An error with
// no wire detail at all is treated as ambiguous: when in doubt, verify rather
// than assume the order was not placed.
//
// domainName is the name the call was about: registrar messages often echo it
// ("mybalance.com is not available"), so it is removed before the message is
// matched against the account/taken wording.
func classify(err error, cfg Config, domainName string) outcome {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		ae = nil // a plain error: keep its own text, not apperr.From's "internal error"
	}
	var code apperr.Code
	if ae != nil {
		code = ae.Code
	}
	ce := ports.RegistrarCallErrorOf(err)
	msg := errText(err, ae, ce)
	matchable := msg
	if domainName != "" {
		matchable = strings.ReplaceAll(strings.ToLower(msg), strings.ToLower(domainName), "")
	}

	if ce != nil {
		if ce.NotSent {
			return outcome{kind: outNotSent, wait: cfg.RateLimitBackoff, msg: msg}
		}
		if ce.Ambiguous {
			return outcome{kind: outAmbiguous, msg: msg}
		}
	}

	status := 0
	if ce != nil {
		status = ce.HTTPStatus
	}
	switch {
	case status == 401 || status == 403:
		return outcome{kind: outPause, msg: "registrar rejected the reseller credentials: " + msg}
	case status == 429 || code == apperr.CodeRateLimited:
		wait := cfg.RateLimitBackoff
		if ce != nil && ce.RetryAfter > wait {
			wait = ce.RetryAfter
		}
		if wait > maxBackoff {
			wait = maxBackoff
		}
		return outcome{kind: outBackoff, wait: wait, msg: msg}
	case status == 409 || code == apperr.CodeConflict:
		return outcome{kind: outTaken, msg: msg}
	case code == apperr.CodeValidation:
		for _, d := range ae.Details {
			if d.Field == "customer_id" {
				return outcome{kind: outFail, msg: "registrar rejected the customer: " + msg}
			}
		}
		return rejected(msg, matchable)
	case ce == nil:
		return outcome{kind: outAmbiguous, msg: msg}
	}
	if status >= 400 && status < 500 {
		return rejected(msg, matchable)
	}
	return outcome{kind: outAmbiguous, msg: msg}
}

// rejected classifies a definitive 4xx refusal that is not a clearer case: the
// reseller running out of money pauses everything (waiting for a top-up), a
// "not available" wording is just the name still being taken, anything else is
// counted against this one snipe. matchable is msg with the domain name
// removed, so a hostile or unlucky name cannot spell out a pause.
func rejected(msg, matchable string) outcome {
	switch {
	case fatalAccountRE.MatchString(matchable):
		return outcome{kind: outPause, msg: "reseller account problem: " + msg, balance: true}
	case takenRE.MatchString(matchable):
		return outcome{kind: outTaken, msg: msg}
	}
	return outcome{kind: outRetry, msg: msg}
}

// errText is the short upstream message worth showing in last_error.
func errText(err error, ae *apperr.Error, ce *ports.RegistrarCallError) string {
	if ce != nil && ce.Message != "" {
		return ce.Message
	}
	if ae != nil && ae.Message != "" {
		return ae.Message
	}
	return err.Error()
}

// isNotFound reports whether err is the registrar's "not in our account".
func isNotFound(err error) bool {
	var ae *apperr.Error
	return errors.As(err, &ae) && ae.Code == apperr.CodeNotFound
}

// isCtxErr reports whether err only exists because ctx was cancelled or timed
// out - the hunt is shutting down (or its own request timer fired), which is
// not a registrar verdict.
func isCtxErr(ctx context.Context, err error) bool {
	return ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

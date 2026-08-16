package main

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Duitku V2 mock. Signature formulas (PRD §8.1):
//
//	Get Payment Method:  SHA256(merchantcode + amount + datetime + apiKey)
//	Inquiry (create tx): MD5(merchantCode + merchantOrderId + paymentAmount + apiKey)
//	Callback (sent):     MD5(merchantCode + amount + merchantOrderId + apiKey)
//	Check Transaction:   MD5(merchantCode + merchantOrderId + apiKey)

const (
	duitkuStatusPending = "pending"
	duitkuStatusPaid    = "paid"
	duitkuStatusExpired = "expired"
)

type duitkuTx struct {
	Reference       string
	MerchantOrderID string
	Amount          int64
	PaymentMethod   string
	ProductDetail   string
	AdditionalParam string
	CallbackURL     string
	ReturnURL       string
	VANumber        string
	QRString        string
	Status          string
	CreatedAt       time.Time
}

func writeDuitkuError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"statusCode":    "XX",
		"statusMessage": message,
	})
}

func writeDuitkuBadSignature(w http.ResponseWriter) {
	writeDuitkuError(w, http.StatusBadRequest, "invalid signature")
}

type duitkuPaymentMethod struct {
	PaymentMethod string `json:"paymentMethod"`
	PaymentName   string `json:"paymentName"`
	PaymentImage  string `json:"paymentImage"`
	TotalFee      string `json:"totalFee"`
}

// duitkuMethods returns the fixed catalog of mock payment methods.
func duitkuMethods() []duitkuPaymentMethod {
	mk := func(code, name string, fee int64) duitkuPaymentMethod {
		return duitkuPaymentMethod{
			PaymentMethod: code,
			PaymentName:   name,
			PaymentImage:  "https://images.duitku.com/hotlink-ok/" + code + ".PNG",
			TotalFee:      strconv.FormatInt(fee, 10),
		}
	}
	return []duitkuPaymentMethod{
		mk("VC", "Credit Card (Visa / Master)", 5000),
		mk("BC", "BCA Virtual Account", 4000),
		mk("M2", "Mandiri Virtual Account", 4000),
		mk("OV", "OVO", 0),
		mk("SA", "ShopeePay Apps", 0),
		mk("SP", "QRIS ShopeePay", 700),
		mk("FT", "Retail Alfamart / Pegadaian / POS", 5000),
		mk("DN", "Indodana Paylater", 0),
	}
}

// Duitku payment-method code families (PRD §8.1: "BC/M2/VA/I1/B1/BT/BR"
// virtual account, "SP/LQ/NQ" QRIS) - real Duitku only populates vaNumber
// for a VA code and qrString for a QRIS code, never both and never for any
// other channel (e-wallet/credit-card/retail/paylater get neither, only
// paymentUrl). Mirrored here so client-driven e2e coverage can actually
// exercise all three inquiry-response shapes, not just the "both present"
// shape a simpler mock would return unconditionally.
func isVAMethod(code string) bool {
	switch code {
	case "BC", "M2", "VA", "I1", "B1", "BT", "BR":
		return true
	}
	return false
}

func isQRISMethod(code string) bool {
	switch code {
	case "SP", "LQ", "NQ":
		return true
	}
	return false
}

// POST /webapi/api/merchant/paymentmethod/getpaymentmethod
func (s *Server) handleDuitkuGetPaymentMethods(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MerchantCode string    `json:"merchantcode"`
		Amount       flexInt64 `json:"amount"`
		Datetime     string    `json:"datetime"`
		Signature    string    `json:"signature"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeDuitkuError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	amountStr := strconv.FormatInt(req.Amount.Value, 10)
	expected := sha256Hex(s.cfg.DuitkuMerchantCode + amountStr + req.Datetime + s.cfg.DuitkuAPIKey)
	if req.MerchantCode != s.cfg.DuitkuMerchantCode || !strings.EqualFold(req.Signature, expected) {
		writeDuitkuBadSignature(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"paymentFee":      duitkuMethods(),
		"responseCode":    "00",
		"responseMessage": "SUCCESS",
	})
}

// POST /webapi/api/merchant/v2/inquiry
func (s *Server) handleDuitkuInquiry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MerchantCode    string    `json:"merchantCode"`
		PaymentAmount   flexInt64 `json:"paymentAmount"`
		PaymentMethod   string    `json:"paymentMethod"`
		MerchantOrderID string    `json:"merchantOrderId"`
		ProductDetails  string    `json:"productDetails"`
		AdditionalParam string    `json:"additionalParam"`
		Email           string    `json:"email"`
		PhoneNumber     string    `json:"phoneNumber"`
		CustomerVaName  string    `json:"customerVaName"`
		CallbackURL     string    `json:"callbackUrl"`
		ReturnURL       string    `json:"returnUrl"`
		Signature       string    `json:"signature"`
		ExpiryPeriod    int       `json:"expiryPeriod"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeDuitkuError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MerchantOrderID == "" {
		writeDuitkuError(w, http.StatusBadRequest, "missing merchantOrderId")
		return
	}
	if !req.PaymentAmount.Set || req.PaymentAmount.Value <= 0 {
		writeDuitkuError(w, http.StatusBadRequest, "invalid paymentAmount")
		return
	}
	// Real Duitku rejects sub-Rp10.000 inquiries on every non-QRIS channel
	// (observed against the sandbox); mirror it so e2e exercises the same
	// rejection the production gateway would produce.
	if req.PaymentAmount.Value < 10000 && !isQRISMethod(req.PaymentMethod) {
		writeDuitkuError(w, http.StatusBadRequest, "Minimum Payment 10000 IDR")
		return
	}
	amountStr := strconv.FormatInt(req.PaymentAmount.Value, 10)
	expected := md5Hex(s.cfg.DuitkuMerchantCode + req.MerchantOrderID + amountStr + s.cfg.DuitkuAPIKey)
	if req.MerchantCode != s.cfg.DuitkuMerchantCode || !strings.EqualFold(req.Signature, expected) {
		writeDuitkuBadSignature(w)
		return
	}

	s.mu.Lock()
	s.duitkuSeq++
	seq := s.duitkuSeq
	tx := &duitkuTx{
		Reference:       fmt.Sprintf("MOCKREF-%d", seq),
		MerchantOrderID: req.MerchantOrderID,
		Amount:          req.PaymentAmount.Value,
		PaymentMethod:   req.PaymentMethod,
		ProductDetail:   req.ProductDetails,
		AdditionalParam: req.AdditionalParam,
		CallbackURL:     req.CallbackURL,
		ReturnURL:       req.ReturnURL,
		Status:          duitkuStatusPending,
		CreatedAt:       time.Now(),
	}
	if isVAMethod(req.PaymentMethod) {
		tx.VANumber = fmt.Sprintf("7007%012d", seq)
	}
	if isQRISMethod(req.PaymentMethod) {
		tx.QRString = "MOCK-QR-" + tx.Reference
	}
	s.duitkuTxs[tx.Reference] = tx
	s.duitkuByOrder[tx.MerchantOrderID] = tx
	s.mu.Unlock()

	resp := map[string]any{
		"merchantCode":  s.cfg.DuitkuMerchantCode,
		"reference":     tx.Reference,
		"paymentUrl":    s.cfg.BaseURL + "/payment/" + tx.Reference,
		"amount":        amountStr,
		"statusCode":    "00",
		"statusMessage": "SUCCESS",
	}
	// vaNumber/qrString are only present at all for their matching channel
	// family, same as real Duitku - never both, never for any other method.
	if tx.VANumber != "" {
		resp["vaNumber"] = tx.VANumber
	}
	if tx.QRString != "" {
		resp["qrString"] = tx.QRString
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /webapi/api/merchant/transactionStatus
func (s *Server) handleDuitkuTransactionStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MerchantCode    string `json:"merchantCode"`
		MerchantOrderID string `json:"merchantOrderId"`
		Signature       string `json:"signature"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeDuitkuError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	expected := md5Hex(s.cfg.DuitkuMerchantCode + req.MerchantOrderID + s.cfg.DuitkuAPIKey)
	if req.MerchantCode != s.cfg.DuitkuMerchantCode || !strings.EqualFold(req.Signature, expected) {
		writeDuitkuBadSignature(w)
		return
	}

	s.mu.Lock()
	tx, ok := s.duitkuByOrder[req.MerchantOrderID]
	var snapshot duitkuTx
	if ok {
		snapshot = *tx
	}
	s.mu.Unlock()
	if !ok {
		writeDuitkuError(w, http.StatusNotFound, "transaction not found")
		return
	}

	statusCode, statusMessage := "01", "PENDING"
	switch snapshot.Status {
	case duitkuStatusPaid:
		statusCode, statusMessage = "00", "SUCCESS"
	case duitkuStatusExpired:
		statusCode, statusMessage = "02", "CANCELED"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchantOrderId": snapshot.MerchantOrderID,
		"reference":       snapshot.Reference,
		"amount":          strconv.FormatInt(snapshot.Amount, 10),
		"fee":             "0",
		"statusCode":      statusCode,
		"statusMessage":   statusMessage,
	})
}

// GET /payment/{reference} - the hosted payment page a user would see.
func (s *Server) handleDuitkuPaymentPage(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("reference")
	s.mu.Lock()
	tx, ok := s.duitkuTxs[ref]
	var snapshot duitkuTx
	if ok {
		snapshot = *tx
	}
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!doctype html>
<html lang="id">
<head><meta charset="utf-8"><title>Duitku Mock Payment</title></head>
<body>
  <h1>Duitku Mock Payment</h1>
  <p>Order: <span id="merchant-order-id">%s</span></p>
  <p>Amount: <span id="amount">Rp%d</span></p>
  <p>Reference: <span id="reference">%s</span></p>
  <p>Status: <span id="status">%s</span></p>
  <form method="POST" action="/mock/duitku/pay/%s">
    <button id="pay-now" type="submit">Bayar Sekarang</button>
  </form>
</body>
</html>`,
		html.EscapeString(snapshot.MerchantOrderID),
		snapshot.Amount,
		html.EscapeString(snapshot.Reference),
		html.EscapeString(snapshot.Status),
		url.PathEscape(snapshot.Reference),
	)
}

// POST /mock/duitku/pay/{reference} - marks the tx paid, delivers the real
// signed callback to the stored callbackUrl (retrying on non-200), then
// redirects the browser to returnUrl (if any).
func (s *Server) handleDuitkuPay(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("reference")
	s.mu.Lock()
	tx, ok := s.duitkuTxs[ref]
	if ok && tx.Status == duitkuStatusExpired {
		s.mu.Unlock()
		writeDuitkuError(w, http.StatusConflict, "transaction expired")
		return
	}
	var snapshot duitkuTx
	if ok {
		tx.Status = duitkuStatusPaid
		snapshot = *tx
	}
	s.mu.Unlock()
	if !ok {
		writeDuitkuError(w, http.StatusNotFound, "transaction not found")
		return
	}

	if snapshot.CallbackURL != "" {
		if err := s.sendDuitkuCallback(snapshot); err != nil {
			// Payment still counts as done; the backend reconciliation cron
			// (Check Transaction) is expected to recover from a lost callback.
			log.Printf("duitku mock: callback delivery to %s failed: %v", snapshot.CallbackURL, err)
		}
	}

	if snapshot.ReturnURL != "" {
		http.Redirect(w, r, buildReturnURL(snapshot), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":          duitkuStatusPaid,
		"reference":       snapshot.Reference,
		"merchantOrderId": snapshot.MerchantOrderID,
	})
}

// POST /mock/duitku/expire/{reference}
func (s *Server) handleDuitkuExpire(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("reference")
	s.mu.Lock()
	tx, ok := s.duitkuTxs[ref]
	if ok && tx.Status == duitkuStatusPaid {
		s.mu.Unlock()
		writeDuitkuError(w, http.StatusConflict, "transaction already paid")
		return
	}
	if ok {
		tx.Status = duitkuStatusExpired
	}
	s.mu.Unlock()
	if !ok {
		writeDuitkuError(w, http.StatusNotFound, "transaction not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": duitkuStatusExpired, "reference": ref})
}

// sendDuitkuCallback POSTs the standard Duitku form-urlencoded callback with a
// valid MD5 signature. Non-200 responses (or transport errors) are retried up
// to 3 times after the initial attempt (4 attempts max) with linear backoff.
func (s *Server) sendDuitkuCallback(tx duitkuTx) error {
	amountStr := strconv.FormatInt(tx.Amount, 10)
	form := url.Values{
		"merchantCode":    {s.cfg.DuitkuMerchantCode},
		"amount":          {amountStr},
		"merchantOrderId": {tx.MerchantOrderID},
		"productDetail":   {tx.ProductDetail},
		"additionalParam": {tx.AdditionalParam},
		"paymentCode":     {tx.PaymentMethod},
		"resultCode":      {"00"},
		"reference":       {tx.Reference},
		"signature":       {md5Hex(s.cfg.DuitkuMerchantCode + amountStr + tx.MerchantOrderID + s.cfg.DuitkuAPIKey)},
	}

	var lastErr error
	const maxAttempts = 4 // 1 initial + 3 retries
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			s.sleep(time.Duration(attempt-1) * 100 * time.Millisecond)
		}
		resp, err := s.httpClient.PostForm(tx.CallbackURL, form)
		if err != nil {
			lastErr = err
			continue
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			return nil
		}
		lastErr = fmt.Errorf("callback returned HTTP %d", status)
	}
	return fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

func buildReturnURL(tx duitkuTx) string {
	u, err := url.Parse(tx.ReturnURL)
	if err != nil {
		return tx.ReturnURL
	}
	q := u.Query()
	q.Set("merchantOrderId", tx.MerchantOrderID)
	q.Set("resultCode", "00")
	q.Set("reference", tx.Reference)
	u.RawQuery = q.Encode()
	return u.String()
}

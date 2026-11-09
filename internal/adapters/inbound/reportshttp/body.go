package reportshttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// Wire bodies (camelCase, apis/openapi.yaml tag `reports`).

type touchesDayBody struct {
	Day                   string  `json:"day"`
	Tier                  string  `json:"tier"`
	PackagesDelivered     int     `json:"packagesDelivered"`
	MeanTouchesPerPackage float64 `json:"meanTouchesPerPackage"`
	P50TouchesPerPackage  float64 `json:"p50TouchesPerPackage"`
	P90TouchesPerPackage  float64 `json:"p90TouchesPerPackage"`
}

type touchesReportBody struct {
	From string           `json:"from"`
	To   string           `json:"to"`
	Days []touchesDayBody `json:"days"`
}

func touchesBody(rg report.Range, rows []report.TouchesRow) touchesReportBody {
	out := touchesReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]touchesDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, touchesDayBody{
			Day: report.FormatDay(r.Day), Tier: r.Tier, PackagesDelivered: r.PackagesDelivered,
			MeanTouchesPerPackage: r.Mean, P50TouchesPerPackage: r.P50, P90TouchesPerPackage: r.P90,
		})
	}
	return out
}

type outcomeDayBody struct {
	Day                             string   `json:"day"`
	PackagesDelivered               int      `json:"packagesDelivered"`
	FirstAttemptDelivered           int      `json:"firstAttemptDelivered"`
	PackagesReturnedToSender        int      `json:"packagesReturnedToSender"`
	PackagesLost                    int      `json:"packagesLost"`
	FirstAttemptSuccessRate         float64  `json:"firstAttemptSuccessRate"`
	MeanAttemptsPerDeliveredPackage *float64 `json:"meanAttemptsPerDeliveredPackage"`
	LostRate                        float64  `json:"lostRate"`
	ReturnToSenderRate              float64  `json:"returnToSenderRate"`
}

type outcomesReportBody struct {
	From string           `json:"from"`
	To   string           `json:"to"`
	Days []outcomeDayBody `json:"days"`
}

func outcomesBody(rg report.Range, rows []report.OutcomeRow) outcomesReportBody {
	out := outcomesReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]outcomeDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, outcomeDayBody{
			Day: report.FormatDay(r.Day), PackagesDelivered: r.Delivered, FirstAttemptDelivered: r.FirstAttemptDelivered,
			PackagesReturnedToSender: r.Returned, PackagesLost: r.Lost, FirstAttemptSuccessRate: r.FirstAttemptSuccessRate,
			MeanAttemptsPerDeliveredPackage: r.MeanAttempts, LostRate: r.LostRate, ReturnToSenderRate: r.ReturnToSenderRate,
		})
	}
	return out
}

type freshnessBody struct {
	AsOf       *time.Time `json:"asOf"`
	LagSeconds *float64   `json:"lagSeconds"`
}

// problem is one RFC 9457 problem type: its status, slug and title.
type problem struct {
	status int
	slug   string
	title  string
}

var (
	invalidRange          = problem{http.StatusBadRequest, "invalid-report-range", "The report range is invalid"}
	notFound              = problem{http.StatusNotFound, "not-found", "Resource not found"}
	methodNotAllowed      = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
	internalError         = problem{http.StatusInternalServerError, "internal-error", "Internal error"}
)

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()})
}

// logSafe strips line breaks so a logged value cannot forge a log line.
var logSafe = strings.NewReplacer("\n", " ", "\r", " ")

// writeInternal logs err and answers a 500 whose detail never leaks it.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", logSafe.Replace(err.Error()), "path", logSafe.Replace(r.URL.Path))
	writeProblem(w, r, internalError, "an unexpected error occurred")
}

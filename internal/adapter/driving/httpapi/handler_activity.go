package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func HandleListActivity(repo output.ActivityRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		if limit <= 0 {
			limit = 50
		}

		activities, err := repo.List(r.Context(), limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, activities)
	}
}

type ActivityStatsResponse struct {
	TodayCost float64 `json:"today_cost"`
	MonthCost float64 `json:"month_cost"`
}

func HandleActivityStats(repo output.ActivityRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

		todayCost, err := repo.GetCostSince(r.Context(), todayStart)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		monthCost, err := repo.GetCostSince(r.Context(), monthStart)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, ActivityStatsResponse{
			TodayCost: todayCost,
			MonthCost: monthCost,
		})
	}
}

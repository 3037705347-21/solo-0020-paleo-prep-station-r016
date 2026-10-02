package httpapi

import "net/http"

func (api *API) listSpecimenEvents(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListEventsForSpecimen(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}

func (api *API) listPlanEvents(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListEventsForPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}

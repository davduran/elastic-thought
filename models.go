package elasticthought

import (
	"github.com/couchbaselabs/logg"
	"github.com/dustin/httputil"
	"github.com/tleyden/go-couch"
)

const (
	DOC_TYPE_USER         = "user"
	DOC_TYPE_DATAFILE     = "datafile"
	DOC_TYPE_DATASET      = "dataset"
	DOC_TYPE_SOLVER       = "solver"
	DOC_TYPE_TRAINING_JOB = "training-job"
	DOC_TYPE_CLASSIFIER   = "classifier"
	DOC_TYPE_CLASSIFY_JOB = "classify-job"
)

// All document structs should embed this struct go get access to
// the sync gateway metadata (_id, _rev) and the "type" field
// which differentiates the different doc types.
type ElasticThoughtDoc struct {
	Revision string `json:"_rev"`
	Id       string `json:"_id"`
	Type     string `json:"type"`
}

// Generic cas update
func casUpdate(db couch.Database, thing2update interface{}, updater func(interface{}), doneMetric func(interface{}) bool, refresh func(interface{}) error) (bool, error) {

	if doneMetric(thing2update) == true {
		logg.LogTo("ELASTIC_THOUGHT", "No update needed: %+v, ignoring", thing2update)
		return false, nil
	}

	for {
		updater(thing2update)

		logg.LogTo("ELASTIC_THOUGHT", "Attempting to save update: %+v", thing2update)
		_, err := db.Edit(thing2update)

		if err != nil {

			// if it failed with any other error than 409, return an error
			if !httputil.IsHTTPStatus(err, 409) {
				logg.LogTo("ELASTIC_THOUGHT", "Update failed with non-409 error: %v", err)
				return false, err
			}

			logg.LogTo("ELASTIC_THOUGHT", "Could not update, going to refresh")

			// get the latest version of the document
			if err := refresh(thing2update); err != nil {
				return false, err
			}

			// does it already have the new the state (eg, someone else set it)?
			if doneMetric(thing2update) == true {
				logg.LogTo("ELASTIC_THOUGHT", "No update needed: %+v, done", thing2update)
				return false, nil
			}

			// no, so try updating state and saving again
			continue

		}

		// successfully saved, we are done
		logg.LogTo("ELASTIC_THOUGHT", "Successfully updated %+v, done", thing2update)
		return true, nil

	}

}

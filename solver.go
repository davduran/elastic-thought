package elasticthought

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/couchbaselabs/cbfs/client"
	"github.com/couchbaselabs/logg"
	"github.com/tleyden/go-couch"
)

// A solver can generate trained models, which ban be used to make predictions
type Solver struct {
	ElasticThoughtDoc
	DatasetId        string `json:"dataset-id"`
	SpecificationUrl string `json:"specification-url" binding:"required"`
}

// Create a new solver.  If you don't use this, you must set the
// embedded ElasticThoughtDoc Type field.
func NewSolver() *Solver {
	return &Solver{
		ElasticThoughtDoc: ElasticThoughtDoc{Type: DOC_TYPE_SOLVER},
	}
}

// Insert into database (only call this if you know it doesn't arleady exist,
// or else you'll end up w/ unwanted dupes)
func (s Solver) Insert(db couch.Database) (*Solver, error) {

	id, _, err := db.Insert(s)
	if err != nil {
		err := fmt.Errorf("Error inserting solver: %v.  Err: %v", s, err)
		return nil, err
	}

	// load dataset object from db (so we have id/rev fields)
	solver := &Solver{}
	err = db.Retrieve(id, solver)
	if err != nil {
		err := fmt.Errorf("Error fetching solver: %v.  Err: %v", id, err)
		return nil, err
	}

	return solver, nil

}

// download contents of solver-spec-url into cbfs://<solver-id>/spec.prototxt
// and update solver object's solver-spec-url with cbfs url
func (s Solver) SaveSpec(db couch.Database, cbfs *cbfsclient.Client) (*Solver, error) {

	// open stream to source url
	url := s.SpecificationUrl
	resp, err := http.Get(url)
	if err != nil {
		errMsg := fmt.Errorf("Error doing GET on: %v.  %v", url, err)
		return nil, errMsg
	}
	defer resp.Body.Close()

	// save to cbfs
	options := cbfsclient.PutOptions{
		ContentType: "text/plain",
	}
	destPath := fmt.Sprintf("%v/spec.prototxt", s.Id)
	if err := cbfs.Put("", destPath, resp.Body, options); err != nil {
		errMsg := fmt.Errorf("Error writing %v to cbfs: %v", destPath, err)
		return nil, errMsg
	}
	logg.LogTo("REST", "Wrote %v to cbfs", destPath)

	// update solver with cbfs url
	cbfsUrl := fmt.Sprintf("%v%v", CBFS_URI_PREFIX, destPath)
	s.SpecificationUrl = cbfsUrl

	// save
	solver, err := s.Save(db)
	if err != nil {
		return nil, err
	}

	return solver, nil
}

// Saves the solver to the db, returns latest rev
func (s Solver) Save(db couch.Database) (*Solver, error) {

	// TODO: retry if 409 error
	_, err := db.Edit(s)
	if err != nil {
		return nil, err
	}

	// load latest version of dataset to return
	solver := &Solver{}
	err = db.Retrieve(s.Id, solver)
	if err != nil {
		return nil, err
	}

	return solver, nil

}

// Save the content in the SpecificationUrl to the given directory.
// As the filename, use the last part of the url path from the SpecificationUrl
func (s Solver) SaveSpecification(config Configuration, destDirectory string) error {

	// strip leading cbfs://
	specUrlPath, err := s.SpecificationUrlPath()
	if err != nil {
		return err
	}

	// get filename, eg, if path is foo/spec.txt, get spec.txt
	_, specUrlFilename := filepath.Split(specUrlPath)

	// use cbfs client to open stream

	cbfs, err := cbfsclient.New(config.CbfsUrl)
	if err != nil {
		return err
	}

	// get from cbfs
	logg.LogTo("TRAINING_JOB", "Cbfs get %v", specUrlPath)
	reader, err := cbfs.Get(specUrlPath)
	if err != nil {
		return err
	}

	// write stream to file in work directory
	destPath := filepath.Join(destDirectory, specUrlFilename)
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	defer w.Flush()
	_, err = io.Copy(w, reader)
	if err != nil {
		return err
	}

	logg.LogTo("TRAINING_JOB", "Wrote to %v", destPath)

	return nil
}

// Download and untar the training and test .tar.gz files associated w/ solver,
// as well as index files.
func (s Solver) SaveTrainTestData(config Configuration, destDirectory string) error {

	// find cbfs paths to artificacts
	dataset := NewDataset()
	dataset.Id = s.DatasetId
	trainingArtifact := dataset.TrainingArtifactPath()
	testArtifact := dataset.TestingArtifactPath()

	artificactPaths := []string{trainingArtifact, testArtifact}
	for _, artificactPath := range artificactPaths {

		// create cbfs client
		cbfs, err := cbfsclient.New(config.CbfsUrl)
		if err != nil {
			return err
		}

		// open stream to artifact in cbfs
		logg.LogTo("TRAINING_JOB", "Cbfs get %v", artificactPath)
		reader, err := cbfs.Get(artificactPath)
		if err != nil {
			return err
		}
		defer reader.Close()

		subdirectory := ""
		destTocFile := ""
		if artificactPath == trainingArtifact {
			subdirectory = "training-data"
			destTocFile = path.Join(destDirectory, "training")
		} else {
			subdirectory = "test-data"
			destTocFile = path.Join(destDirectory, "test")
		}
		destDirectoryToUse := path.Join(destDirectory, subdirectory)

		toc, err := untarGzWithToc(reader, destDirectoryToUse)
		tocWithLabels := addLabelsToToc(toc)
		tocWithSubdir := addParentDirToToc(tocWithLabels, subdirectory)

		for _, tocEntry := range tocWithSubdir {
			logg.LogTo("TRAINING_JOB", "tocEntry %v", tocEntry)
		}
		if err != nil {
			return err
		}

		writeTocToFile(tocWithSubdir, destTocFile)

	}
	return nil

}

func writeTocToFile(toc []string, destFile string) error {
	f, err := os.Create(destFile)
	if err != nil {
		logg.LogTo("SOLVER", "calling os.Create failed on %v", destFile)
		return err
	}
	w := bufio.NewWriter(f)
	defer w.Flush()
	for _, tocEntry := range toc {
		tocEntryNewline := fmt.Sprintf("%v\n", tocEntry)
		if _, err := w.WriteString(tocEntryNewline); err != nil {
			return err
		}
	}

	return nil
}

/*
Given a toc:

    Q/Verdana-5-0.png 27
    R/Arial-5-0.png 28

And a parent dir, eg, "training-data", generate a new TOC:

    training-data/Q/Verdana-5-0.png 27
    training-data/R/Arial-5-0.png 28

*/
func addParentDirToToc(tableOfContents []string, dir string) []string {

	tocWithDir := []string{}
	for _, tocEntry := range tableOfContents {
		components := strings.Split(tocEntry, " ")
		file := components[0]
		label := components[1]
		file = path.Join(dir, file)
		line := fmt.Sprintf("%v %v", file, label)
		tocWithDir = append(tocWithDir, line)
	}

	return tocWithDir

}

/*
Given a toc:

    Q/Verdana-5-0.png
    R/Arial-5-0.png

Add a numeric label to each line, eg:

    Q/Verdana-5-0.png 27
    R/Arial-5-0.png 28

Where the label starts at 0 and is incremented for
each new directory found.

*/
func addLabelsToToc(tableOfContents []string) []string {

	currentDirectory := ""
	labelIndex := 0
	tocWithLabels := []string{}

	for _, tocEntry := range tableOfContents {

		dir := path.Dir(tocEntry)
		logg.LogTo("SOLVER", dir)

		if currentDirectory == "" {
			// we're on the first directory
			currentDirectory = dir
		} else {
			// we're not on the first directory, but
			// are we on a new directory?
			if dir == currentDirectory {
				// nope, use curentLabelIndex
			} else {
				// yes, so increment label index
				labelIndex += 1
			}
			currentDirectory = dir
		}

		tocEntryWithLabel := fmt.Sprintf("%v %v", tocEntry, labelIndex)
		tocWithLabels = append(tocWithLabels, tocEntryWithLabel)

	}

	return tocWithLabels

}

// If spefication url is "cbfs://foo/bar.txt", return "/foo/bar.txt"
func (s Solver) SpecificationUrlPath() (string, error) {

	specUrl := s.SpecificationUrl
	if !strings.HasPrefix(specUrl, CBFS_URI_PREFIX) {
		return "", fmt.Errorf("Expected %v to start with %v", specUrl, CBFS_URI_PREFIX)
	}

	return strings.Replace(specUrl, CBFS_URI_PREFIX, "", 1), nil

}
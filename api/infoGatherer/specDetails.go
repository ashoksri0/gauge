// Copyright 2015 ThoughtWorks, Inc.

// This file is part of Gauge.

// Gauge is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Gauge is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with Gauge.  If not, see <http://www.gnu.org/licenses/>.

package infoGatherer

import (
	"io/ioutil"
	"path/filepath"
	"sync"

	"github.com/getgauge/common"
	"github.com/getgauge/gauge/config"
	"github.com/getgauge/gauge/gauge"
	"github.com/getgauge/gauge/gauge_messages"
	"github.com/getgauge/gauge/logger"
	"github.com/getgauge/gauge/parser"
	"github.com/getgauge/gauge/util"
	"github.com/golang/protobuf/proto"
	fsnotify "gopkg.in/fsnotify.v1"
)

// SpecInfoGatherer contains the caches for specs, concepts, and steps
type SpecInfoGatherer struct {
	waitGroup         sync.WaitGroup
	mutex             sync.Mutex
	conceptDictionary *gauge.ConceptDictionary
	specsCache        map[string][]*gauge.Specification
	conceptsCache     map[string][]*gauge.Concept
	stepsCache        map[string]*gauge.StepValue
	SpecDirs          []string
}

// MakeListOfAvailableSteps initializes all the SpecInfoGatherer caches
func (s *SpecInfoGatherer) MakeListOfAvailableSteps() {
	go s.watchForFileChanges()
	s.waitGroup.Wait()

	// Concepts parsed first because we need to create a concept dictionary that spec parsing can use
	s.waitGroup.Add(3)
	s.initConceptsCache()
	s.initSpecsCache()
	s.initStepsCache()
}

func (s *SpecInfoGatherer) initSpecsCache() {
	defer s.waitGroup.Done()

	var specFiles []string
	s.specsCache = make(map[string][]*gauge.Specification, 0)

	if len(s.SpecDirs) < 1 {
		specFiles = util.FindSpecFilesIn(filepath.Join(config.ProjectRoot, common.SpecsDirectoryName))
	} else {
		for _, dir := range s.SpecDirs {
			specFiles = append(specFiles, util.FindSpecFilesIn(filepath.Join(config.ProjectRoot, dir))...)
		}
	}

	parsedSpecs := s.getParsedSpecs(specFiles)

	logger.APILog.Info("Initializing specs cache with %d specs", len(parsedSpecs))
	for _, spec := range parsedSpecs {
		logger.APILog.Debug("Adding specs from %s", spec.FileName)
		s.addToSpecsCache(spec.FileName, spec)
	}
}

func (s *SpecInfoGatherer) initConceptsCache() {
	defer s.waitGroup.Done()

	s.conceptsCache = make(map[string][]*gauge.Concept, 0)
	parsedConcepts := s.getParsedConcepts()

	logger.APILog.Info("Initializing concepts cache with %d concepts", len(parsedConcepts))
	for _, concept := range parsedConcepts {
		logger.APILog.Debug("Adding concepts from %s", concept.FileName)
		s.addToConceptsCache(concept.FileName, concept)
	}
}

func (s *SpecInfoGatherer) initStepsCache() {
	defer s.waitGroup.Done()

	s.stepsCache = make(map[string]*gauge.StepValue, 0)
	stepsFromSpecs := s.getStepsFromCachedSpecs()
	stepsFromConcepts := s.getStepsFromCachedConcepts()

	allSteps := append(stepsFromSpecs, stepsFromConcepts...)

	logger.APILog.Info("Initializing steps cache with %d steps", len(allSteps))
	s.addToStepsCache(allSteps)
}

func (s *SpecInfoGatherer) addToSpecsCache(key string, value *gauge.Specification) {
	s.mutex.Lock()
	if s.specsCache[key] == nil {
		s.specsCache[key] = make([]*gauge.Specification, 0)
	}
	s.specsCache[key] = append(s.specsCache[key], value)
	s.mutex.Unlock()
}

func (s *SpecInfoGatherer) addToConceptsCache(key string, value *gauge.Concept) {
	s.mutex.Lock()
	if s.conceptsCache[key] == nil {
		s.conceptsCache[key] = make([]*gauge.Concept, 0)
	}
	s.conceptsCache[key] = append(s.conceptsCache[key], value)
	s.mutex.Unlock()
}

func (s *SpecInfoGatherer) addToStepsCache(allSteps []*gauge.StepValue) {
	s.mutex.Lock()
	for _, step := range allSteps {
		if _, ok := s.stepsCache[step.StepValue]; !ok {
			s.stepsCache[step.StepValue] = step
		}
	}
	s.mutex.Unlock()
}

func (s *SpecInfoGatherer) getParsedSpecs(specFiles []string) []*gauge.Specification {
	if s.conceptDictionary == nil {
		s.conceptDictionary = gauge.NewConceptDictionary()
	}
	parsedSpecs, parseResults := parser.ParseSpecFiles(specFiles, s.conceptDictionary)
	handleParseFailures(parseResults)
	return parsedSpecs
}

func (s *SpecInfoGatherer) getParsedConcepts() map[string]*gauge.Concept {
	var result *parser.ParseResult
	s.conceptDictionary, result = parser.CreateConceptsDictionary(true, s.SpecDirs)
	handleParseFailures([]*parser.ParseResult{result})
	return s.conceptDictionary.ConceptsMap
}

func (s *SpecInfoGatherer) getStepsFromCachedSpecs() []*gauge.StepValue {
	var stepValues []*gauge.StepValue
	s.mutex.Lock()
	for _, specList := range s.specsCache {
		for _, spec := range specList {
			stepValues = append(stepValues, getStepsFromSpec(spec)...)
		}
	}
	s.mutex.Unlock()
	return stepValues
}

func (s *SpecInfoGatherer) getStepsFromCachedConcepts() []*gauge.StepValue {
	var stepValues []*gauge.StepValue
	s.mutex.Lock()
	for _, conceptList := range s.conceptsCache {
		for _, concept := range conceptList {
			stepValues = append(stepValues, getStepsFromConcept(concept)...)
		}
	}
	s.mutex.Unlock()
	return stepValues
}

func (s *SpecInfoGatherer) onSpecFileModify(file string) {
	s.waitGroup.Add(1)
	defer s.waitGroup.Done()

	logger.APILog.Info("Spec file added / modified: %s", file)
	parsedSpecs := s.getParsedSpecs([]string{file})
	if len(parsedSpecs) != 0 {
		parsedSpec := parsedSpecs[0]
		s.addToSpecsCache(file, parsedSpec)
		stepsFromSpec := getStepsFromSpec(parsedSpec)
		s.addToStepsCache(stepsFromSpec)
	}
}

func (s *SpecInfoGatherer) onConceptFileModify(file string) {
	s.waitGroup.Add(1)
	defer s.waitGroup.Done()

	logger.APILog.Info("Concept file added / modified: %s", file)
	conceptParser := new(parser.ConceptParser)
	concepts, parseResults := conceptParser.ParseFile(file)
	if parseResults != nil && parseResults.Error != nil {
		logger.APILog.Error("Error parsing concepts: ", parseResults.Error)
		return
	}

	for _, concept := range concepts {
		c := gauge.Concept{ConceptStep: concept, FileName: file}
		s.addToConceptsCache(file, &c)
		stepsFromConcept := getStepsFromConcept(&c)
		s.addToStepsCache(stepsFromConcept)
	}
}

func (s *SpecInfoGatherer) onSpecFileRemove(file string) {
	s.waitGroup.Add(1)
	defer s.waitGroup.Done()

	logger.APILog.Info("Spec file removed: %s", file)
	s.mutex.Lock()
	delete(s.specsCache, file)
	s.mutex.Unlock()
}

func (s *SpecInfoGatherer) onConceptFileRemove(file string) {
	s.waitGroup.Add(1)
	defer s.waitGroup.Done()

	logger.APILog.Info("Concept file removed: %s", file)
	s.mutex.Lock()
	delete(s.conceptsCache, file)
	s.mutex.Unlock()
}

func (s *SpecInfoGatherer) onFileAdd(watcher *fsnotify.Watcher, file string) {
	if util.IsDir(file) {
		addDirToFileWatcher(watcher, file)
	}
	s.onFileModify(watcher, file)
}

func (s *SpecInfoGatherer) onFileModify(watcher *fsnotify.Watcher, file string) {
	if util.IsSpec(file) {
		s.onSpecFileModify(file)
	} else if util.IsConcept(file) {
		s.onConceptFileModify(file)
	}
}

func (s *SpecInfoGatherer) onFileRemove(watcher *fsnotify.Watcher, file string) {
	if util.IsSpec(file) {
		s.onSpecFileRemove(file)
	} else if util.IsConcept(file) {
		s.onConceptFileRemove(file)
	} else {
		removeWatcherOn(watcher, file)
	}
}

func (s *SpecInfoGatherer) onFileRename(watcher *fsnotify.Watcher, file string) {
	s.onFileRemove(watcher, file)
}

func (s *SpecInfoGatherer) handleEvent(event fsnotify.Event, watcher *fsnotify.Watcher) {
	s.waitGroup.Wait()

	file, err := filepath.Abs(event.Name)
	if err != nil {
		logger.APILog.Error("Failed to get abs file path for %s: %s", event.Name, err)
		return
	}
	if util.IsSpec(file) || util.IsConcept(file) {
		switch event.Op {
		case fsnotify.Create:
			s.onFileAdd(watcher, file)
		case fsnotify.Write:
			s.onFileModify(watcher, file)
		case fsnotify.Rename:
			s.onFileRename(watcher, file)
		case fsnotify.Remove:
			s.onFileRemove(watcher, file)
		}
	}
}

func (s *SpecInfoGatherer) watchForFileChanges() {
	s.waitGroup.Add(1)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.APILog.Error("Error creating fileWatcher: %s", err)
	}
	defer watcher.Close()

	done := make(chan bool)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				s.handleEvent(event, watcher)
			case err := <-watcher.Errors:
				logger.APILog.Error("Error event while watching specs", err)
			}
		}
	}()

	var allDirsToWatch []string
	var specDir string

	if len(s.SpecDirs) < 1 {
		specDir = filepath.Join(config.ProjectRoot, common.SpecsDirectoryName)
		allDirsToWatch = append(allDirsToWatch, specDir)
		allDirsToWatch = append(allDirsToWatch, util.FindAllNestedDirs(specDir)...)
	} else {
		for _, dir := range s.SpecDirs {
			specDir = filepath.Join(config.ProjectRoot, dir)
			allDirsToWatch = append(allDirsToWatch, specDir)
			allDirsToWatch = append(allDirsToWatch, util.FindAllNestedDirs(specDir)...)
		}
	}

	for _, dir := range allDirsToWatch {
		addDirToFileWatcher(watcher, dir)
	}
	s.waitGroup.Done()
	<-done
}

// GetAvailableSpecs returns the list of all the specs in the gauge project
func (s *SpecInfoGatherer) GetAvailableSpecs() []*gauge.Specification {
	s.waitGroup.Wait()

	var allSpecs []*gauge.Specification
	s.mutex.Lock()
	for _, specs := range s.specsCache {
		allSpecs = append(allSpecs, specs...)
	}
	s.mutex.Unlock()
	return allSpecs
}

// GetAvailableSteps returns the list of all the steps in the gauge project
func (s *SpecInfoGatherer) GetAvailableSteps() []*gauge.StepValue {
	s.waitGroup.Wait()

	var steps []*gauge.StepValue
	s.mutex.Lock()
	for _, stepValue := range s.stepsCache {
		steps = append(steps, stepValue)
	}
	s.mutex.Unlock()
	return steps
}

// GetConceptInfos returns an array containing information about all the concepts present in the Gauge project
func (s *SpecInfoGatherer) GetConceptInfos() []*gauge_messages.ConceptInfo {
	s.waitGroup.Wait()

	var conceptInfos []*gauge_messages.ConceptInfo
	s.mutex.Lock()
	for _, conceptList := range s.conceptsCache {
		for _, concept := range conceptList {
			stepValue := parser.CreateStepValue(concept.ConceptStep)
			conceptInfos = append(conceptInfos, &gauge_messages.ConceptInfo{StepValue: gauge.ConvertToProtoStepValue(&stepValue), Filepath: proto.String(concept.FileName), LineNumber: proto.Int(concept.ConceptStep.LineNo)})
		}
	}
	s.mutex.Unlock()
	return conceptInfos
}

func getStepsFromSpec(spec *gauge.Specification) []*gauge.StepValue {
	stepValues := getParsedStepValues(spec.Contexts)
	for _, scenario := range spec.Scenarios {
		stepValues = append(stepValues, getParsedStepValues(scenario.Steps)...)
	}
	return stepValues
}

func getStepsFromConcept(concept *gauge.Concept) []*gauge.StepValue {
	return getParsedStepValues(concept.ConceptStep.ConceptSteps)
}

func getParsedStepValues(steps []*gauge.Step) []*gauge.StepValue {
	var stepValues []*gauge.StepValue
	for _, step := range steps {
		if !step.IsConcept {
			stepValue := parser.CreateStepValue(step)
			stepValues = append(stepValues, &stepValue)
		}
	}
	return stepValues
}

func handleParseFailures(parseResults []*parser.ParseResult) {
	for _, result := range parseResults {
		if !result.Ok {
			logger.APILog.Error("Spec Parse failure: %s", result.Error())
		}
	}
}

func addDirToFileWatcher(watcher *fsnotify.Watcher, dir string) {
	err := watcher.Add(dir)
	if err != nil {
		logger.APILog.Error("Unable to add directory %v to file watcher: %s", dir, err)
	} else {
		logger.APILog.Info("Watching directory: %s", dir)
		files, _ := ioutil.ReadDir(dir)
		logger.APILog.Debug("Found %d files", len(files))
	}
}

func removeWatcherOn(watcher *fsnotify.Watcher, path string) {
	logger.APILog.Info("Removing watcher on : %s", path)
	watcher.Remove(path)
}

func createGetStepNamesRequest() *gauge_messages.Message {
	return &gauge_messages.Message{MessageType: gauge_messages.Message_StepNamesRequest.Enum(), StepNamesRequest: &gauge_messages.StepNamesRequest{}}
}

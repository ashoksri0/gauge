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

package gauge

import (
	"reflect"
)

type HeadingType int

const (
	SpecHeading     = 0
	ScenarioHeading = 1
)

type TokenKind int

const (
	SpecKind TokenKind = iota
	TagKind
	ScenarioKind
	CommentKind
	StepKind
	TableHeader
	TableRow
	HeadingKind
	TableKind
	DataTableKind
	TearDownKind
)

type Specification struct {
	Heading       *Heading
	Scenarios     []*Scenario
	Comments      []*Comment
	DataTable     DataTable
	Contexts      []*Step
	FileName      string
	Tags          *Tags
	Items         []Item
	TearDownSteps []*Step
}

type Item interface {
	Kind() TokenKind
}

func (spec *Specification) Kind() TokenKind {
	return SpecKind
}

func (spec *Specification) ProcessConceptStepsFrom(conceptDictionary *ConceptDictionary) error {
	for _, step := range spec.Contexts {
		if err := spec.processConceptStep(step, conceptDictionary); err != nil {
			return err
		}
	}
	for _, scenario := range spec.Scenarios {
		for _, step := range scenario.Steps {
			if err := spec.processConceptStep(step, conceptDictionary); err != nil {
				return err
			}
		}
	}
	for _, step := range spec.TearDownSteps {
		if err := spec.processConceptStep(step, conceptDictionary); err != nil {
			return err
		}
	}
	return nil
}

func (spec *Specification) processConceptStep(step *Step, conceptDictionary *ConceptDictionary) error {
	if conceptFromDictionary := conceptDictionary.Search(step.Value); conceptFromDictionary != nil {
		return spec.createConceptStep(conceptFromDictionary.ConceptStep, step)
	}
	return nil
}

func (spec *Specification) createConceptStep(concept *Step, originalStep *Step) error {
	stepCopy, err := concept.GetCopy()
	if err != nil {
		return err
	}
	originalArgs := originalStep.Args
	originalStep.CopyFrom(stepCopy)
	originalStep.Args = originalArgs

	// set parent of all concept steps to be the current concept (referred as originalStep here)
	// this is used to fetch from parent's lookup when nested
	for _, conceptStep := range originalStep.ConceptSteps {
		conceptStep.Parent = originalStep
	}

	return spec.PopulateConceptLookup(&originalStep.Lookup, concept.Args, originalStep.Args)
}

func (spec *Specification) AddItem(itemToAdd Item) {
	if spec.Items == nil {
		spec.Items = make([]Item, 0)
	}

	spec.Items = append(spec.Items, itemToAdd)
}

func (spec *Specification) AddHeading(heading *Heading) {
	heading.HeadingType = SpecHeading
	spec.Heading = heading
}

func (spec *Specification) AddScenario(scenario *Scenario) {
	spec.Scenarios = append(spec.Scenarios, scenario)
	spec.AddItem(scenario)
}

func (spec *Specification) AddContext(contextStep *Step) {
	spec.Contexts = append(spec.Contexts, contextStep)
	spec.AddItem(contextStep)
}

func (spec *Specification) AddComment(comment *Comment) {
	spec.Comments = append(spec.Comments, comment)
	spec.AddItem(comment)
}

func (spec *Specification) AddDataTable(table *Table) {
	spec.DataTable.Table = *table
	spec.AddItem(&spec.DataTable)
}

func (spec *Specification) AddExternalDataTable(externalTable *DataTable) {
	spec.DataTable = *externalTable
	spec.AddItem(externalTable)
}

func (spec *Specification) AddTags(tags *Tags) {
	spec.Tags = tags
	spec.AddItem(spec.Tags)
}

func (spec *Specification) NTags() int {
	if spec.Tags == nil {
		return 0
	}
	return len(spec.Tags.Values())
}

func (spec *Specification) LatestScenario() *Scenario {
	return spec.Scenarios[len(spec.Scenarios)-1]
}

func (spec *Specification) LatestContext() *Step {
	return spec.Contexts[len(spec.Contexts)-1]
}

func (spec *Specification) LatestTeardown() *Step {
	return spec.TearDownSteps[len(spec.TearDownSteps)-1]
}

func (spec *Specification) removeItem(itemIndex int) {
	item := spec.Items[itemIndex]
	if len(spec.Items)-1 == itemIndex {
		spec.Items = spec.Items[:itemIndex]
	} else if 0 == itemIndex {
		spec.Items = spec.Items[itemIndex+1:]
	} else {
		spec.Items = append(spec.Items[:itemIndex], spec.Items[itemIndex+1:]...)
	}
	if item.Kind() == ScenarioKind {
		spec.removeScenario(item.(*Scenario))
	}
}

func (spec *Specification) removeScenario(scenario *Scenario) {
	index := getIndexFor(scenario, spec.Scenarios)
	if len(spec.Scenarios)-1 == index {
		spec.Scenarios = spec.Scenarios[:index]
	} else if index == 0 {
		spec.Scenarios = spec.Scenarios[index+1:]
	} else {
		spec.Scenarios = append(spec.Scenarios[:index], spec.Scenarios[index+1:]...)
	}
}

func (spec *Specification) PopulateConceptLookup(lookup *ArgLookup, conceptArgs []*StepArg, stepArgs []*StepArg) error {
	for i, arg := range stepArgs {
		stepArg := StepArg{Value: arg.Value, ArgType: arg.ArgType, Table: arg.Table, Name: arg.Name}
		if err := lookup.AddArgValue(conceptArgs[i].Value, &stepArg); err != nil {
			return err
		}
	}
	return nil
}

func (spec *Specification) RenameSteps(oldStep Step, newStep Step, orderMap map[int]int) bool {
	isRefactored := spec.rename(spec.Contexts, oldStep, newStep, false, orderMap)
	for _, scenario := range spec.Scenarios {
		refactor := scenario.renameSteps(oldStep, newStep, orderMap)
		if refactor {
			isRefactored = refactor
		}
	}
	return spec.rename(spec.TearDownSteps, oldStep, newStep, isRefactored, orderMap)
}

func (spec *Specification) rename(steps []*Step, oldStep Step, newStep Step, isRefactored bool, orderMap map[int]int) bool {
	isConcept := false
	for _, step := range steps {
		isRefactored = step.Rename(oldStep, newStep, isRefactored, orderMap, &isConcept)
	}
	return isRefactored
}

func (spec *Specification) GetSpecItems() []Item {
	specItems := make([]Item, 0)
	for _, item := range spec.Items {
		if item.Kind() != ScenarioKind {
			specItems = append(specItems, item)
		}
	}
	return specItems
}

func (spec *Specification) Traverse(processor ItemProcessor, queue *ItemQueue) {
	processor.Specification(spec)
	processor.Heading(spec.Heading)

	for queue.Peek() != nil {
		item := queue.Next()
		switch item.Kind() {
		case ScenarioKind:
			processor.Heading(item.(*Scenario).Heading)
			processor.Scenario(item.(*Scenario))
		case StepKind:
			processor.Step(item.(*Step))
		case CommentKind:
			processor.Comment(item.(*Comment))
		case TableKind:
			processor.Table(item.(*Table))
		case TagKind:
			processor.Tags(item.(*Tags))
		case TearDownKind:
			processor.TearDown(item.(*TearDown))
		case DataTableKind:
			processor.DataTable(item.(*DataTable))
		}
	}
}

func (spec *Specification) AllItems() (items []Item) {
	for _, item := range spec.Items {
		items = append(items, item)
		if item.Kind() == ScenarioKind {
			items = append(items, item.(*Scenario).Items...)
		}
	}
	return
}

func (spec *Specification) UsesArgsInContextTeardown(args ...string) bool {
	return UsesArgs(append(spec.Contexts, spec.TearDownSteps...), args...)
}

type SpecItemFilter interface {
	Filter(Item) bool
}

func (spec *Specification) Filter(filter SpecItemFilter) {
	for i := 0; i < len(spec.Items); i++ {
		if filter.Filter(spec.Items[i]) {
			spec.removeItem(i)
			i--
		}
	}
}

func getIndexFor(scenario *Scenario, scenarios []*Scenario) int {
	for index, anItem := range scenarios {
		if reflect.DeepEqual(scenario, anItem) {
			return index
		}
	}
	return -1
}

type Heading struct {
	Value       string
	LineNo      int
	HeadingType HeadingType
}

func (heading *Heading) Kind() TokenKind {
	return HeadingKind
}

type Comment struct {
	Value  string
	LineNo int
}

func (comment *Comment) Kind() TokenKind {
	return CommentKind
}

type TearDown struct {
	LineNo int
	Value  string
}

func (t *TearDown) Kind() TokenKind {
	return TearDownKind
}

type Tags struct {
	RawValues [][]string
}

func (tags *Tags) Add(values []string) {
	tags.RawValues = append(tags.RawValues, values)
}

func (tags *Tags) Values() (val []string) {
	for i, _ := range tags.RawValues {
		val = append(val, tags.RawValues[i]...)
	}
	return val
}
func (tags *Tags) Kind() TokenKind {
	return TagKind
}

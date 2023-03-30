// This program generate part of table inside markdown file.
// Generated table rows have format:
// | <parameter-name> | <required> | <description> |

// The markdown content between html comments "<!-- FUNCTION-SPEC-START -->" and "<!-- FUNCTION-SPEC-END -->"
// will be replaced but rows with "<!-- KEEP-THIS -->" at the end will be preserved.
// Special tags (html comments) must be at the end of lines due to markdown requirements.
// All comments in markdown file that contain "<!-- SKIP-ELEMENT -->" and the name of the element
// will skip the generation of that element. Similarly with "<!-- SKIP-WITH-ANCESTORS -->" comments,
// which skip elements along with their descendants.
// Words in descriptions that are surrounded by asterisks will be replaced with parameter names
// with full path and surrounded by double asterisks.

// Generated documentation is equivalent with:
// kubectl explain --api-version='serverless.kyma-project.io/v1alpha2' function

package main

import (
	"fmt"
	"os"
	"regexp"
	"sigs.k8s.io/yaml"
	"sort"
	"strings"
)

var APIVersion = os.Args[1]
var CRDFilename = os.Args[2]
var MDFilename = os.Args[3]

const FunctionSpecIdentifier = `FUNCTION-SPEC`
const REFunctionSpecPattern = `(?s)<!--\s*` + FunctionSpecIdentifier + `-START\s* -->.*<!--\s*` + FunctionSpecIdentifier + `-END\s*-->`

const KeepThisIdentifier = `KEEP-THIS`
const REKeepThisPattern = `[^\S\r\n]*[|]\s*\*{2}([^*]+)\*{2}.*<!--\s*` + KeepThisIdentifier + `\s*-->`

const SkipIdentifier = `SKIP-ELEMENT`
const RESkipPattern = `<!--\s*` + SkipIdentifier + `\s*([^\s]+)\s*-->`
const SkipWithAncestorsIdentifier = `SKIP-WITH-ANCESTORS`
const RESkipWithAncestorsPattern = `<!--\s*` + SkipWithAncestorsIdentifier + `\s*([^\s-]+)\s*-->`

const REElementInDescription = `\*(\w+)\*`

type FunctionSpecGenerator struct {
	elementsToKeep map[string]string
	elementsToSkip map[string]bool
}

func main() {
	toKeep := getElementsToKeep()
	toSkip := getElementsToSkip()
	gen := CreateFunctionSpecGenerator(toKeep, toSkip)
	doc := gen.generateDocFromCRD()
	replaceDocInMD(doc)
}

func getElementsToKeep() map[string]string {
	inDoc, err := os.ReadFile(MDFilename)
	if err != nil {
		panic(err)
	}

	reFunSpec := regexp.MustCompile(REFunctionSpecPattern)
	funSpecPart := reFunSpec.FindString(string(inDoc))
	reKeep := regexp.MustCompile(REKeepThisPattern)
	rowsToKeep := reKeep.FindAllStringSubmatch(funSpecPart, -1)

	toKeep := map[string]string{}
	for _, pair := range rowsToKeep {
		rowContent := pair[0]
		paramName := pair[1]
		toKeep[paramName] = rowContent
	}
	return toKeep
}

func getElementsToSkip() map[string]bool {
	inDoc, err := os.ReadFile(MDFilename)
	if err != nil {
		panic(err)
	}

	doc := string(inDoc)
	reSkip := regexp.MustCompile(RESkipPattern)
	toSkip := map[string]bool{}
	for _, pair := range reSkip.FindAllStringSubmatch(doc, -1) {
		paramName := pair[1]
		toSkip[paramName] = false
	}

	reSkipWithAncestors := regexp.MustCompile(RESkipWithAncestorsPattern)
	for _, pair := range reSkipWithAncestors.FindAllStringSubmatch(doc, -1) {
		paramName := pair[1]
		toSkip[paramName] = true
	}

	return toSkip
}

func replaceDocInMD(doc string) {
	inDoc, err := os.ReadFile(MDFilename)
	if err != nil {
		panic(err)
	}

	newContent := strings.Join([]string{
		"<!-- " + FunctionSpecIdentifier + "-START -->",
		doc + "<!-- " + FunctionSpecIdentifier + "-END -->",
	}, "\n")
	re := regexp.MustCompile(REFunctionSpecPattern)
	outDoc := re.ReplaceAll(inDoc, []byte(newContent))

	outFile, err := os.OpenFile(MDFilename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		panic(err)
	}
	defer outFile.Close()
	outFile.Write(outDoc)
}

func CreateFunctionSpecGenerator(toKeep map[string]string, toSkip map[string]bool) FunctionSpecGenerator {
	return FunctionSpecGenerator{
		elementsToKeep: toKeep,
		elementsToSkip: toSkip,
	}
}

func (g *FunctionSpecGenerator) generateDocFromCRD() string {
	input, err := os.ReadFile(CRDFilename)
	if err != nil {
		panic(err)
	}

	// why unmarshalling to CustomResource don't work?
	var obj interface{}
	if err := yaml.Unmarshal(input, &obj); err != nil {
		panic(err)
	}

	docElements := map[string]string{}
	versions := getElement(obj, "spec", "versions")
	for _, version := range versions.([]interface{}) {
		name := getElement(version, "name")
		if name.(string) != APIVersion {
			continue
		}
		functionSpec := getElement(version, "schema", "openAPIV3Schema", "properties", "spec")
		for k, v := range g.generateElementDoc(functionSpec, "spec", true, "") {
			docElements[k] = v
		}
	}

	for k, v := range g.elementsToKeep {
		docElements[k] = v
	}

	var doc []string
	for _, propName := range sortedKeys(docElements) {
		doc = append(doc, docElements[propName])
	}
	return strings.Join(doc, "\n")
}

func (g *FunctionSpecGenerator) generateElementDoc(obj interface{}, name string, required bool, parentPath string) map[string]string {
	result := map[string]string{}
	element := obj.(map[string]interface{})
	elementType := element["type"].(string)
	description := ""
	if d := element["description"]; d != nil {
		description = d.(string)
	}

	fullName := fmt.Sprintf("%s%s", parentPath, name)
	skipWithAncestors, shouldBeSkipped := g.elementsToSkip[fullName]
	if shouldBeSkipped && skipWithAncestors {
		return result
	}
	_, isRowToKeep := g.elementsToKeep[fullName]
	if !shouldBeSkipped && !isRowToKeep {
		description = normalizeDescription(description, name)
		description = expandElementLinksInDescription(description, parentPath)
		result[fullName] =
			fmt.Sprintf("| **%s** | %s | %s |",
				fullName, yesNo(required), description)
	}

	if elementType == "object" {
		for k, v := range g.generateObjectDoc(element, name, parentPath) {
			result[k] = v
		}
	}
	return result
}

func (g *FunctionSpecGenerator) generateObjectDoc(element map[string]interface{}, name string, parentPath string) map[string]string {
	result := map[string]string{}
	properties := getElement(element, "properties")
	if properties == nil {
		return result
	}

	var requiredChildren []interface{}
	if rc := getElement(element, "required"); rc != nil {
		requiredChildren = rc.([]interface{})
	}

	propMap := properties.(map[string]interface{})
	for _, propName := range sortedKeys(propMap) {
		propRequired := contains(requiredChildren, name)
		for k, v := range g.generateElementDoc(propMap[propName], propName, propRequired, parentPath+name+".") {
			result[k] = v
		}
	}
	return result
}

func getElement(obj interface{}, path ...string) interface{} {
	elem := obj
	for _, p := range path {
		elem = elem.(map[string]interface{})[p]
	}
	return elem
}

func normalizeDescription(description string, name string) string {
	d := strings.Trim(description, " ")
	n := strings.Trim(name, " ")
	if len(n) == 0 {
		return d
	}
	dParts := strings.SplitN(d, " ", 2)
	if len(dParts) < 2 {
		return description
	}
	if !strings.EqualFold(n, dParts[0]) {
		return description
	}
	d = strings.Trim(dParts[1], " ")
	d = strings.ToUpper(d[:1]) + d[1:]
	return d
}

func expandElementLinksInDescription(description string, parentPath string) string {
	newContent := fmt.Sprintf("**" + parentPath + "$1**")
	re := regexp.MustCompile(REElementInDescription)
	return re.ReplaceAllString(description, newContent)
}

func sortedKeys[T any](propMap map[string]T) []string {
	var keys []string
	for key := range propMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func contains(s []interface{}, e string) bool {
	for _, a := range s {
		if a.(string) == e {
			return true
		}
	}
	return false
}

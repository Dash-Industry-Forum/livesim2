package patch

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/beevik/etree"
)

// PatchExpirationMargin is added to the HTTP expiration beyond ttl.
const PatchExpirationMargin = 10 * time.Second

var ErrPatchSamePublishTime = fmt.Errorf("same publishTime in both MPDs")
var ErrPatchTooLate = fmt.Errorf("patch TTL exceeded")

type patchDoc struct {
	doc *etree.Document
}

func newPatchDoc(oldRoot, newRoot *etree.Element) (*patchDoc, error) {
	oldMpdId := getAttrValue(oldRoot, "id")
	newMpdId := getAttrValue(newRoot, "id")
	if oldMpdId == "" || newMpdId != oldMpdId {
		return nil, fmt.Errorf("not the same non-empty id in both MPDs")
	}
	oldPublishTime := getAttrValue(oldRoot, "publishTime")
	if oldPublishTime == "" {
		return nil, fmt.Errorf("no publishTime attribute in old MPD")
	}
	newPublishTime := getAttrValue(newRoot, "publishTime")
	if newPublishTime == "" {
		return nil, fmt.Errorf("no publishTime attribute in new MPD")
	}
	root := etree.NewElement("Patch")
	root.CreateAttr("xmlns", "urn:mpeg:dash:schema:mpd-patch:2020")
	root.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
	root.CreateAttr("xsi:schemaLocation", "urn:mpeg:dash:schema:mpd-patch:2020 DASH-MPD-PATCH.xsd")
	root.CreateAttr("mpdId", oldMpdId)
	root.CreateAttr("originalPublishTime", oldPublishTime)
	root.CreateAttr("publishTime", newPublishTime)
	doc := etree.NewDocument()
	doc.CreateProcInst("xml", `version="1.0" encoding="UTF-8"`)
	doc.SetRoot(root)
	return &patchDoc{
		doc: doc,
	}, nil
}

// MPDDiff compares two MPDs and returns a patch document or an error.
func MPDDiff(mpdOld, mpdNew []byte) (doc *etree.Document, expiration time.Time, err error) {
	dOld := etree.NewDocument()
	err = dOld.ReadFromBytes(mpdOld)
	if err != nil {
		return nil, expiration, fmt.Errorf("failed to read old MPD: %w", err)
	}
	dNew := etree.NewDocument()
	err = dNew.ReadFromBytes(mpdNew)
	if err != nil {
		return nil, expiration, fmt.Errorf("failed to read new MPD: %w", err)
	}
	oldRoot := dOld.Root()
	newRoot := dNew.Root()

	expiration, err = checkPatchConditions(oldRoot, newRoot)
	if err != nil {
		return nil, expiration, err
	}

	pDoc, err := newPatchDoc(oldRoot, newRoot)
	if err != nil {
		return nil, expiration, fmt.Errorf("failed to create patch doc: %w", err)
	}

	elemPath := "/MPD"
	root := pDoc.doc.Root()
	err = addElemChanges(root, oldRoot, newRoot, elemPath)
	if err != nil {
		return nil, expiration, err
	}

	return pDoc.doc, expiration, nil
}

func checkPatchConditions(oldRoot, newRoot *etree.Element) (expiration time.Time, err error) {
	if oldRoot.Tag != "MPD" || newRoot.Tag != "MPD" {
		return expiration, fmt.Errorf("not MPD root element in both MPDs")
	}
	newPublishTime := getAttrValue(newRoot, "publishTime")
	oldPublishTime := getAttrValue(oldRoot, "publishTime")
	if newPublishTime == "" || oldPublishTime == "" {
		return expiration, fmt.Errorf("lacking publishTime attribute in MPD")
	}
	if newPublishTime == oldPublishTime {
		return expiration, ErrPatchSamePublishTime
	}
	oldPatchLocation := oldRoot.SelectElement("PatchLocation")
	if oldPatchLocation == nil {
		return expiration, fmt.Errorf("no PatchLocation element in old MPD")
	}
	oldTTL := oldPatchLocation.SelectAttr("ttl")
	if oldTTL == nil {
		return expiration, fmt.Errorf("no ttl attribute in PatchLocation element in old MPD")
	}
	ttl, err := strconv.Atoi(oldTTL.Value)
	if err != nil {
		return expiration, fmt.Errorf("failed to convert ttl attribute in PatchLocation element in old MPD: %w", err)
	}
	oldPT, err := time.Parse(time.RFC3339, oldPublishTime)
	if err != nil {
		return expiration, fmt.Errorf("failed to parse old publishTime: %w", err)
	}
	newPT, err := time.Parse(time.RFC3339, newPublishTime)
	if err != nil {
		return expiration, fmt.Errorf("failed to parse new publishTime: %w", err)
	}
	expiration = oldPT.Add(time.Duration(ttl)*time.Second + PatchExpirationMargin)
	if newPT.After(expiration) {
		return expiration, ErrPatchTooLate
	}
	return expiration, nil
}

// getAttrValue returns value if key exists, of empty string
func getAttrValue(e *etree.Element, key string) string {
	a := e.SelectAttr(key)
	if a == nil {
		return ""
	}
	return a.Value
}

// addAttrChanges adds changes to patchRoot for attributes in newElem relative to oldElem
func addAttrChanges(patchRoot, oldElem, newElem *etree.Element, elemPath string) error {
	changes, err := compareAttributes(oldElem.Attr, newElem.Attr)
	if err != nil {
		return err
	}
	for _, a := range changes.Changed {
		e := patchRoot.CreateElement("replace")
		e.CreateAttr("sel", fmt.Sprintf("%s/@%s", elemPath, a.Key))
		e.SetText(a.Value)
	}
	for _, a := range changes.Added {
		e := patchRoot.CreateElement("add")
		e.CreateAttr("sel", fmt.Sprintf("%s/@%s", elemPath, a.Key))
		e.SetText(a.Value)
	}
	for _, a := range changes.Removed {
		e := patchRoot.CreateElement("remove")
		e.CreateAttr("sel", fmt.Sprintf("%s/@%s", elemPath, a.Key))
		e.SetText(a.Value)
	}
	return nil
}

// addElemChanges adds changes to patchRoot for elements in new relative to old
// elemPath is the path to the element in the MPD needed for the patch
func addElemChanges(patchRoot, old, new *etree.Element, elemPath string) error {
	if old.Tag != new.Tag {
		return fmt.Errorf("different tags %q and %q", old.Tag, new.Tag)
	}
	err := checkMandatoryIdAttribute(old)
	if err != nil {
		return err
	}
	err = checkMandatoryIdAttribute(new)
	if err != nil {
		return err
	}
	if old.Tag == "SegmentTimeline" {
		return addLeafListChanges(patchRoot, old, new, elemPath)
	}
	if isLeaf(old) && isLeaf(new) {
		return addLeafChanges(patchRoot, old, new, elemPath)
	}
	err = addAttrChanges(patchRoot, old, new, elemPath)
	if err != nil {
		return fmt.Errorf("addAttrChanges for %s: %w", elemPath, err)
	}
	// Not leaf, so we should compare children)
	oldChildren := old.ChildElements()
	newChildren := new.ChildElements()
	diffOps := MyersDiff(oldChildren, newChildren, sameElements)
	oldIdx := 0
	newIdx := 0
	lastNewPath := ""
	lastNewIdx := make(map[string]int) // Last index for each tag
	for _, d := range diffOps {
		for d.OldPos > oldIdx {
			oldElem := oldChildren[oldIdx]
			tag := oldElem.Tag
			addr := calcAddr(oldElem, lastNewIdx[tag])
			newElemPath := fmt.Sprintf("%s/%s", elemPath, addr)
			err := addElemChanges(patchRoot, oldChildren[oldIdx], newChildren[newIdx], newElemPath)
			if err != nil {
				return fmt.Errorf("addElemChanges for %s: %w", newElemPath, err)
			}
			lastNewIdx[tag]++
			lastNewPath = newElemPath
			oldIdx++
			newIdx++
		}
		if d.OldPos == oldIdx {
			switch d.OpType {
			case OpDelete:
				e := patchRoot.CreateElement("remove")
				oldElem := oldChildren[d.OldPos]
				addr := calcAddr(oldElem, oldIdx)
				e.CreateAttr("sel", fmt.Sprintf("%s/%s", elemPath, addr))
				oldIdx++
			case OpInsert:
				e := patchRoot.CreateElement("add")
				newElem := newChildren[d.NewPos]
				addr := calcAddr(newElem, lastNewIdx[newElem.Tag])
				newPath := fmt.Sprintf("%s/%s", elemPath, addr)
				if lastNewPath == "" {
					// Always use prepend on insertion at start (since the list may be empty)
					e.CreateAttr("sel", elemPath)
					e.CreateAttr("pos", "prepend")
				} else {
					// Put after previous element
					e.CreateAttr("sel", lastNewPath)
					e.CreateAttr("pos", "after")
				}
				e.AddChild(newElem.Copy())
				lastNewPath = newPath
				lastNewIdx[newElem.Tag]++
				newIdx++
			}
		}
	}
	// Elements to keep at end after differences
	for oldIdx < len(oldChildren) {
		oldElem := oldChildren[oldIdx]
		newElem := newChildren[newIdx]
		addr := calcAddr(oldElem, lastNewIdx[oldElem.Tag])
		newElemPath := fmt.Sprintf("%s/%s", elemPath, addr)
		err := addElemChanges(patchRoot, oldElem, newElem, newElemPath)
		if err != nil {
			return fmt.Errorf("addElemChanges for %s: %w", newElemPath, err)
		}
		lastNewIdx[oldElem.Tag]++
		oldIdx++
		newIdx++
	}
	return nil
}

func isLeaf(e *etree.Element) bool {
	return len(e.ChildElements()) == 0
}

func addLeafChanges(patchRoot, old, new *etree.Element, elemPath string) error {
	if old.Text() != new.Text() {
		e := patchRoot.CreateElement("replace")
		e.CreateAttr("sel", elemPath)
		n := new.Copy()
		e.AddChild(n)
		return nil
	}
	return addAttrChanges(patchRoot, old, new, elemPath)
}

// addLeafListChanges creates a list of leaf node changes (add or remove)
func addLeafListChanges(patchRoot, old, new *etree.Element, elemPath string) error {
	oldElems := old.ChildElements()
	newElems := new.ChildElements()
	var tag string // Require that all elements have same type
	switch {
	case len(oldElems) == 0 && len(newElems) == 0:
		return nil
	case len(oldElems) > 0:
		tag = oldElems[0].Tag
	default:
		tag = newElems[0].Tag
	}
	for _, e := range oldElems {
		if e.Tag != tag {
			return fmt.Errorf("other tag %q in list of %q", e.Tag, tag)
		}
		if len(e.ChildElements()) > 0 {
			return fmt.Errorf("leaf list element %q has children", tag)
		}
	}
	for _, e := range newElems {
		if e.Tag != tag {
			return fmt.Errorf("other tag %q in list of %q", e.Tag, tag)
		}
		if len(e.ChildElements()) > 0 {
			return fmt.Errorf("leaf list element %q has children", tag)
		}
	}
	diffOps := MyersDiff(oldElems, newElems, equalLeafs)
	// Output removed, added, and check equal elements for possible replacements
	oldIdx := 0
	offset := 0
	for _, d := range diffOps {
		for d.OldPos > oldIdx {
			// Elements to keep at start. Look for changes at lower level
			oldIdx++
		}
		if d.OldPos == oldIdx {
			switch d.OpType {
			case OpDelete:
				e := patchRoot.CreateElement("remove")
				oldElem := oldElems[d.OldPos]
				addr := calcAddr(oldElem, oldIdx+offset)
				e.CreateAttr("sel", fmt.Sprintf("%s/%s", elemPath, addr))
				oldIdx++
				offset--
			case OpInsert:
				e := patchRoot.CreateElement("add")
				newElem := newElems[d.NewPos]
				newPos := oldIdx + offset
				if newPos == 0 {
					// Always use prepend on insertion at start (since the list may be empty)
					e.CreateAttr("sel", elemPath)
					e.CreateAttr("pos", "prepend")
					e.AddChild(newElem.Copy())
				} else {
					// Put after previous element
					addr := calcAddr(newElem, newPos-1)
					e.CreateAttr("sel", fmt.Sprintf("%s/%s", elemPath, addr))
					e.CreateAttr("pos", "after")
					e.AddChild(newElem.Copy())
				}
				offset++
			}
		}
	}
	return nil
}

// calcAddr returns an address for an element in the MPD.
// Uses id, or schemeIdUri if present, and no index for SegmentTimeline and SegmentTemplate.
// Otherwise use numerical index converted to one-based.
func calcAddr(elem *etree.Element, elemIdx int) string {
	if id := getAttrValue(elem, "id"); id != "" {
		return fmt.Sprintf("%s[@id='%s']", elem.Tag, id)
	}
	if schemeIdUri := getAttrValue(elem, "schemeIdUri"); schemeIdUri != "" {
		return fmt.Sprintf("%s[@schemeIdUri='%s']", elem.Tag, schemeIdUri)
	}
	switch elem.Tag {
	case "SegmentTimeline", "SegmentTemplate":
		return elem.Tag
	}
	return fmt.Sprintf("%s[%d]", elem.Tag, elemIdx+1)
}

type attrChange struct {
	Added   []etree.Attr
	Removed []etree.Attr
	Changed []etree.Attr
}

func compareAttributes(old, new []etree.Attr) (attrChange, error) {
	sortAttr(old)
	sortAttr(new)
	oIdx := 0
	nIdx := 0
	var a attrChange

	for oIdx < len(old) || nIdx < len(new) {
		cmp := 0
		if oIdx < len(old) && nIdx < len(new) {
			cmp = compareAttrNames(old[oIdx], new[nIdx])
		} else if oIdx < len(old) {
			cmp = -1
		} else {
			cmp = 1
		}
		switch cmp {
		case -1:
			a.Removed = append(a.Removed, old[oIdx])
			oIdx++
		case 0:
			if old[oIdx].Value != new[nIdx].Value {
				a.Changed = append(a.Changed, new[nIdx])
			}
			oIdx++
			nIdx++
		case 1:
			a.Added = append(a.Added, new[nIdx])
			nIdx++
		}
	}
	return a, nil
}

// Returns -1 if a1 is less than a2, 0 if equal, 1 if greater
func compareAttrNames(a1, a2 etree.Attr) int {
	if a1.Space < a2.Space {
		return -1
	}
	if a1.Space > a2.Space {
		return 1
	}
	if a1.Key < a2.Key {
		return -1
	}
	if a1.Key > a2.Key {
		return 1
	}
	return 0
}

func sortAttr(a []etree.Attr) {
	sort.Slice(a, func(i, j int) bool {
		return compareAttrNames(a[i], a[j]) <= 0
	})
}

// checkMandatoryIdAttribute checks if mandatory id attribute is present in both elements
func checkMandatoryIdAttribute(elem *etree.Element) error {
	switch elem.Tag {
	case "MPD", "Period", "AdaptationSet", "Representation", "SubRepresentation":
		if elem.SelectAttr("id") == nil {
			return fmt.Errorf("id attribute missing in %s", elem.Tag)
		}
	}
	return nil
}

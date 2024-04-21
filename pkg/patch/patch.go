package patch

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/beevik/etree"
)

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
func MPDDiff(mpdOld, mpdNew []byte) (*etree.Document, error) {
	dOld := etree.NewDocument()
	err := dOld.ReadFromBytes(mpdOld)
	if err != nil {
		return nil, fmt.Errorf("failed to read old MPD: %w", err)
	}
	dNew := etree.NewDocument()
	err = dNew.ReadFromBytes(mpdNew)
	if err != nil {
		return nil, fmt.Errorf("failed to read new MPD: %w", err)
	}
	oldRoot := dOld.Root()
	newRoot := dNew.Root()

	err = checkPatchConditions(oldRoot, newRoot)
	if err != nil {
		return nil, err
	}

	pDoc, err := newPatchDoc(oldRoot, newRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch doc: %w", err)
	}

	elemPath := "/MPD"
	root := pDoc.doc.Root()
	err = addElemChanges(root, oldRoot, newRoot, elemPath)
	if err != nil {
		return nil, err
	}

	return pDoc.doc, nil
}

func checkPatchConditions(oldRoot, newRoot *etree.Element) error {
	if oldRoot.Tag != "MPD" || newRoot.Tag != "MPD" {
		return fmt.Errorf("not MPD root element in both MPDs")
	}
	newPublishTime := getAttrValue(newRoot, "publishTime")
	oldPublishTime := getAttrValue(oldRoot, "publishTime")
	if newPublishTime == "" || oldPublishTime == "" {
		return fmt.Errorf("lacking publishTime attribute in MPD")
	}
	if newPublishTime == oldPublishTime {
		return ErrPatchSamePublishTime
	}
	oldPatchLocation := oldRoot.SelectElement("PatchLocation")
	if oldPatchLocation == nil {
		return fmt.Errorf("no PatchLocation element in old MPD")
	}
	oldTTL := oldPatchLocation.SelectAttr("ttl")
	if oldTTL == nil {
		return fmt.Errorf("no ttl attribute in PatchLocation element in old MPD")
	}
	ttl, err := strconv.Atoi(oldTTL.Value)
	if err != nil {
		return fmt.Errorf("failed to convert ttl attribute in PatchLocation element in old MPD: %w", err)
	}
	oldPT, err := time.Parse(time.RFC3339, oldPublishTime)
	if err != nil {
		return fmt.Errorf("failed to parse old publishTime: %w", err)
	}
	newPT, err := time.Parse(time.RFC3339, newPublishTime)
	if err != nil {
		return fmt.Errorf("failed to parse new publishTime: %w", err)
	}
	endTime := oldPT.Add(2 * time.Duration(ttl) * time.Second)
	if newPT.After(endTime) {
		return ErrPatchTooLate
	}
	return nil
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

type elementCounter struct {
	latestTag string
	counter   int
}

func (ec *elementCounter) update(tag string) {
	if ec.latestTag == tag {
		ec.counter++
		return
	}
	ec.latestTag = tag
	ec.counter = 0
}

// index returns a 0-based index for an element
func (ec *elementCounter) index(tag string) int {
	if ec.latestTag == tag {
		return ec.counter + 1
	}
	return 0
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
	ec := elementCounter{}
	newIdx := 0
	for _, d := range diffOps {
		for d.OldPos > oldIdx {
			// Elements to keep at start. Look for changes at lower level
			newElemPath := fmt.Sprintf("%s/%s[%d]", elemPath, oldChildren[oldIdx].Tag, oldIdx)
			err := addElemChanges(patchRoot, oldChildren[oldIdx], newChildren[oldIdx], newElemPath)
			if err != nil {
				return fmt.Errorf("addElemChanges for %s: %w", newElemPath, err)
			}
			ec.update(oldChildren[oldIdx].Tag)
			oldIdx++
			newIdx++
		}
		if d.OldPos == oldIdx {
			switch d.OpType {
			case OpDelete:
				e := patchRoot.CreateElement("remove")
				oldElem := oldChildren[d.OldPos]
				addr := calcAddr(oldElem, ec.index(oldElem.Tag))
				e.CreateAttr("sel", fmt.Sprintf("%s/%s", elemPath, addr))
				oldIdx++
			case OpInsert:
				e := patchRoot.CreateElement("add")
				newElem := newChildren[d.NewPos]
				addr := calcAddr(newElem, ec.index(newElem.Tag))
				e.CreateAttr("sel", fmt.Sprintf("%s/%s[%d]", elemPath, addr, oldIdx))
				e.AddChild(newElem.Copy())
				newIdx++
			}
		}
	}
	// Elements to keep at end after differences
	for oldIdx < len(oldChildren) {
		oldElem := oldChildren[oldIdx]
		newElem := newChildren[newIdx]
		addr := calcAddr(oldElem, ec.index(oldElem.Tag))
		newElemPath := fmt.Sprintf("%s/%s", elemPath, addr)
		err := addElemChanges(patchRoot, oldElem, newElem, newElemPath)
		if err != nil {
			return fmt.Errorf("addElemChanges for %s: %w", newElemPath, err)
		}
		ec.update(oldElem.Tag)
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
	tag := oldElems[0].Tag // Require that all are the same
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

	for {
		if oIdx >= len(old) && nIdx >= len(new) {
			break
		}
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

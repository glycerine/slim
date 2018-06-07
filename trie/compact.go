package trie

import (
	"encoding/binary"
	"unsafe"
	"xec/bit"
	"xec/sparse"
)

const WordMask = 0xf
const MaxWord = 0x10

type CompactTrie struct {
	Children sparse.SparseArray
	Steps    sparse.SparseArray
	Leaves   sparse.SparseArray
}

type children struct {
	Bitmap uint16
	Offset uint16
}

type ChildConv struct {
}

func (c ChildConv) MarshalElt(d interface{}) []byte {
	child := d.(children)

	b := make([]byte, 4)
	binary.LittleEndian.PutUint16(b[:2], child.Bitmap)
	binary.LittleEndian.PutUint16(b[2:4], child.Offset)

	return b
}

func (c ChildConv) UnmarshalElt(b []byte) (uint32, interface{}) {
	d := children{
		Bitmap: binary.LittleEndian.Uint16(b[:2]),
		Offset: binary.LittleEndian.Uint16(b[2:4]),
	}
	return uint32(4), d
}

func (c ChildConv) GetMarshaledEltSize(b []byte) uint32 {
	return uint32(4)
}

func (st *CompactTrie) Compact(root *Node) (err error) {
	if root == nil {
		return
	}

	childIndex, childData := []uint32{}, []children{}
	stepIndex, stepData := []uint32{}, []uint16{}
	leafIndex, leafData := []uint32{}, []interface{}{}

	tq := make([]*Node, 0, 256)
	tq = append(tq, root)

	for nId := uint16(0); ; {
		if len(tq) == 0 {
			break
		}

		node := tq[0]
		tq = tq[1:]

		if len(node.Branches) == 0 {
			continue
		}

		brs := node.Branches

		if brs[0] == leafBranch {
			leafIndex = append(leafIndex, uint32(nId))
			leafData = append(leafData, node.Children[brs[0]].Value)

			brs = brs[1:]
		}

		if node.Step > 1 {
			stepIndex = append(stepIndex, uint32(nId))
			stepData = append(stepData, node.Step)

		}

		if len(brs) > 0 {
			childIndex = append(childIndex, uint32(nId))
			offset := nId + uint16(len(tq)) + uint16(1)

			bitmap := uint16(0)
			for _, b := range brs {
				bitmap |= uint16(1) << (uint16(b) & WordMask)
			}

			ch := children{
				Bitmap: bitmap,
				Offset: offset,
			}

			childData = append(childData, ch)
		}

		for _, b := range brs {
			tq = append(tq, node.Children[b])
		}

		nId++
	}

	err = st.Children.Init(childIndex, childData)
	if err != nil {
		return err
	}

	err = st.Steps.Init(stepIndex, stepData)
	if err != nil {
		return err
	}

	err = st.Leaves.Init(leafIndex, leafData)
	if err != nil {
		return err
	}

	return nil
}

func (st *CompactTrie) Search(key []byte, mode Mode) (value interface{}) {
	eqIdx, ltIdx, gtIdx := int32(0), int32(-1), int32(-1)
	ltLeaf := false

	for idx := uint16(0); ; {
		var word byte
		if uint16(len(key)) == idx {
			word = byte(MaxWord)
		} else {
			word = (uint8(key[idx]) & WordMask)
		}

		li, ei, ri, leaf := st.neighborBranches(uint16(eqIdx), word)
		if li >= 0 {
			ltIdx = li
			ltLeaf = leaf
		}

		if ri >= 0 {
			gtIdx = ri
		}

		eqIdx = ei
		if eqIdx == -1 {
			break
		}

		if word == MaxWord {
			break
		}

		idx += st.getStep(uint16(eqIdx))

		if idx > uint16(len(key)) {
			gtIdx = eqIdx
			eqIdx = -1
			break
		}
	}

	if mode&LT == LT && ltIdx != -1 {
		if ltLeaf {
			value = st.Leaves.Get(uint32(ltIdx))
		} else {
			rmIdx := st.rightMost(uint16(ltIdx))
			value = st.Leaves.Get(uint32(rmIdx))
		}
	}
	if mode&GT == GT && gtIdx != -1 {
		fmIdx := st.leftMost(uint16(gtIdx))
		value = st.Leaves.Get(uint32(fmIdx))
	}
	if mode&EQ == EQ && eqIdx != -1 {
		value = st.Leaves.Get(uint32(eqIdx))
	}

	return
}

func (st *CompactTrie) getChild(idx uint16) *children {
	cval := st.Children.Get(uint32(idx))
	if cval == nil {
		return nil
	}
	ch := cval.(children)

	return &ch
}

func (st *CompactTrie) getStep(idx uint16) uint16 {
	step := st.Steps.Get(uint32(idx))
	if step == nil {
		return uint16(1)
	} else {
		return step.(uint16)
	}
}

func getChildIdx(ch *children, offset uint16) uint16 {
	chNum := bit.Cnt1Before(uint64(ch.Bitmap), uint32(offset))
	return ch.Offset + uint16(chNum) - uint16(1)
}

func (st *CompactTrie) neighborBranches(idx uint16, word byte) (ltIdx, eqIdx, rtIdx int32, ltLeaf bool) {
	ltIdx, eqIdx, rtIdx = int32(-1), int32(-1), int32(-1)
	ltLeaf = false

	leaf := st.Leaves.Get(uint32(idx))

	if word == MaxWord {
		if leaf != nil {
			eqIdx = int32(idx)
		}
	} else {
		if leaf != nil {
			ltIdx = int32(idx)
			ltLeaf = true
		}
	}

	ch := st.getChild(idx)
	if ch == nil {
		return
	}

	if (ch.Bitmap >> word & 1) == 1 {
		eqIdx = int32(getChildIdx(ch, uint16(word+1)))
	}

	ltStart := uint8(word) & WordMask
	for i := int8(ltStart) - 1; i >= 0; i-- {
		if (ch.Bitmap >> uint8(i) & 1) == 1 {
			ltIdx = int32(getChildIdx(ch, uint16(i+1)))
			ltLeaf = false
			break
		}
	}

	rtStart := word + 1
	if word == MaxWord {
		rtStart = uint8(0)
	}

	for i := rtStart; i < MaxWord; i++ {
		if (ch.Bitmap >> i & 1) == 1 {
			rtIdx = int32(getChildIdx(ch, uint16(i+1)))
			break
		}
	}

	return
}

func (st *CompactTrie) leftMost(idx uint16) uint16 {
	for {
		if st.Leaves.Get(uint32(idx)) != nil {
			return idx
		}

		ch := st.getChild(idx)
		idx = ch.Offset
	}
}

func (st *CompactTrie) rightMost(idx uint16) uint16 {
	offset := uint16(unsafe.Sizeof(uint16(0)) * 8)
	for {
		ch := st.getChild(idx)
		if ch == nil {
			return idx
		}

		idx = getChildIdx(ch, offset)
	}
}
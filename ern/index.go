// This file is part of the go-meta library.
//
// Copyright (C) 2017 JAAK MUSIC LTD
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
// If you have any questions please contact yo@jaak.io

package ern

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipld-format"
	"github.com/mattn/go-sqlite3"
	"github.com/meta-network/go-meta"
)

// Indexer is a META indexer which indexes a stream of META objects
// representing DDEX ERNs into a SQLite3 database, getting the
// associated META objects from a META store.
type Indexer struct {
	index *meta.Index
	store *meta.Store
}

// NewIndexer returns an Indexer which updates the indexes in the given SQLite3
// database connection, getting META objects from the given META store.
func NewIndexer(index *meta.Index, store *meta.Store) (*Indexer, error) {
	// migrate the db to ensure it has an up-to-date schema
	if err := migrations.Run(index.DB); err != nil {
		return nil, err
	}

	return &Indexer{
		index: index,
		store: store,
	}, nil
}

// Index indexes a stream of META object links which are expected to
// point at DDEX ERNs.
func (i *Indexer) Index(ctx context.Context, stream *meta.StreamReader) error {
	return i.index.Update(func(tx *sql.Tx) error {
		for {
			select {
			case cid, ok := <-stream.Ch():
				if !ok {
					return stream.Err()
				}
				obj, err := i.store.Get(cid)
				if err != nil {
					return err
				}
				if err := i.indexERN(tx, obj); err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
}

// indexERN indexes a DDEX ERN based on its MessageHeader, WorkList,
// ResourceList and ReleaseList.
func (i *Indexer) indexERN(tx *sql.Tx, ern *meta.Object) error {
	graph := meta.NewGraph(i.store, ern)

	for field, indexFn := range map[string]func(*sql.Tx, *cid.Cid, *meta.Object) error{
		"MessageHeader": i.indexMessageHeader,
		"WorkList":      i.indexWorkList,
		"ResourceList":  i.indexResourceList,
		"ReleaseList":   i.indexReleaseList,
	} {
		v, err := graph.Get("NewReleaseMessage", field)
		if meta.IsPathNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		id, ok := v.(*cid.Cid)
		if !ok {
			return fmt.Errorf("unexpected field type for %q, expected *cid.Cid, got %T", field, v)
		}
		if err := i.indexProperty(tx, ern.Cid(), id, indexFn); err != nil {
			return err
		}
	}

	return nil
}

// indexProperty indexes a particular ERN property using the provided index
// function.
func (i *Indexer) indexProperty(tx *sql.Tx, ernID, cid *cid.Cid, indexFn func(*sql.Tx, *cid.Cid, *meta.Object) error) error {
	obj, err := i.store.Get(cid)
	if err != nil {
		return err
	}
	return indexFn(tx, ernID, obj)
}

// isUniqueErr determines whether an error is a SQLite3 uniqueness error.
func isUniqueErr(err error) bool {
	e, ok := err.(sqlite3.Error)
	if !ok {
		return false
	}
	return e.Code == sqlite3.ErrConstraint && e.ExtendedCode == sqlite3.ErrConstraintUnique
}

// DecodeObj decodes whatever is stored at path into the given value
func DecodeObj(metaStore *meta.Store, metaObj *meta.Object, v interface{}, path ...string) (err error) {
	graph := meta.NewGraph(metaStore, metaObj)

	defer func() {
		if err != nil {
			err = fmt.Errorf("Error decoding %s into %T: %s", path, v, err)
		}
	}()

	x, err := graph.Get(path...)
	if err != nil {
		return err
	}
	id, ok := x.(*cid.Cid)
	if !ok {
		return fmt.Errorf("Expected %s to be *cid.Cid, got %T", path, x)
	}

	obj, err := metaStore.Get(id)
	if err != nil {
		return err
	}
	return obj.Decode(v)
}

// insertParty inserts the PartyName and PartyId fields of a Party object into
// the party index.
func (i *Indexer) insertParty(tx *sql.Tx, obj *meta.Object) error {
	var partyID struct {
		Value string `json:"@value"`
	}
	// explicitly ignore the returned error as it is ok for the PartyId to
	// be missing
	_ = DecodeObj(i.store, obj, &partyID, "PartyId")

	var partyName struct {
		Value string `json:"@value"`
	}
	if err := DecodeObj(i.store, obj, &partyName, "PartyName", "FullName"); err != nil {
		return err
	}
	_, err := tx.Exec(
		"INSERT INTO party (cid, id, name) VALUES ($1, $2, $3)",
		obj.Cid().String(), partyID.Value, partyName.Value,
	)
	if err != nil && !isUniqueErr(err) {
		return err
	}
	return nil
}

// insertParties loads parties from the given field and inserts them into the
// party index.
func (i *Indexer) insertParties(tx *sql.Tx, obj *meta.Object, field string) ([]*cid.Cid, error) {
	var ids []*cid.Cid
	v, err := obj.Get(field)
	if err != nil {
		return nil, err
	}
	switch v := v.(type) {
	case *format.Link:
		ids = []*cid.Cid{v.Cid}
	case []interface{}:
		for _, x := range v {
			id, ok := x.(*cid.Cid)
			if !ok {
				return nil, fmt.Errorf("invalid party type %T, expected *cid.Cid", x)
			}
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		party, err := i.store.Get(id)
		if err != nil {
			return nil, err
		}
		if err := i.insertParty(tx, party); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// indexMessageHeader indexes an ERN MessageHeader based on its MessageId,
// MessageThreadId, MessageSender, MessageRecipient and MessageCreatedDateTime.
func (i *Indexer) indexMessageHeader(tx *sql.Tx, ernID *cid.Cid, obj *meta.Object) error {
	senders, err := i.insertParties(tx, obj, "MessageSender")
	if err != nil {
		return err
	}
	if len(senders) != 1 {
		return fmt.Errorf("expected 1 sender, got %d", len(senders))
	}
	sender := senders[0]

	recipients, err := i.insertParties(tx, obj, "MessageRecipient")
	if err != nil {
		return err
	}
	if len(recipients) != 1 {
		return fmt.Errorf("expected 1 recipient, got %d", len(recipients))
	}
	recipient := recipients[0]

	// get the MessageId, MessageThreadId and MessageCreatedDateTime
	// values
	var messageID struct {
		Value string `json:"@value"`
	}
	if err := DecodeObj(i.store, obj, &messageID, "MessageId"); err != nil {
		return err
	}
	var threadID struct {
		Value string `json:"@value"`
	}
	if err := DecodeObj(i.store, obj, &threadID, "MessageThreadId"); err != nil {
		return err
	}
	var created struct {
		Value string `json:"@value"`
	}
	if err := DecodeObj(i.store, obj, &created, "MessageCreatedDateTime"); err != nil {
		return err
	}

	// update the ERN index
	_, err = tx.Exec(
		"INSERT INTO ern (cid, message_id, thread_id, sender_id, recipient_id, created) VALUES ($1, $2, $3, $4, $5, $6)",
		ernID.String(), messageID.Value, threadID.Value, sender.String(), recipient.String(), created.Value,
	)
	return err
}

func (i *Indexer) indexWorkList(tx *sql.Tx, ernID *cid.Cid, obj *meta.Object) error {
	// TODO: index MusicalWorks
	return nil
}

// indexResourceList indexes an ERN ResourceList based on SoundRecordings.
func (i *Indexer) indexResourceList(tx *sql.Tx, ernID *cid.Cid, obj *meta.Object) error {
	// the SoundRecording property can either be a link if there is only
	// one SoundRecording in the list, or an array of links if there are
	// multiple SoundRecordings in the list, so we need to handle both
	// cases
	v, err := obj.Get("SoundRecording")
	if err != nil {
		return err
	}
	var cids []*cid.Cid
	switch v := v.(type) {
	case *format.Link:
		cids = []*cid.Cid{v.Cid}
	case []interface{}:
		for _, x := range v {
			cid, ok := x.(*cid.Cid)
			if !ok {
				return fmt.Errorf("invalid resource type %T, expected *cid.Cid", x)
			}
			cids = append(cids, cid)
		}
	}

	// load and index each SoundRecording link
	for _, cid := range cids {
		obj, err := i.store.Get(cid)
		if err != nil {
			return err
		}
		if err := i.indexSoundRecording(tx, ernID, obj); err != nil {
			return err
		}
	}

	return nil
}

// indexSoundRecording indexes an ERN SoundRecording based on its ID (either an
// ISRC, CatalogNumber or ProprietaryId) and its ReferenceTitle.
func (i *Indexer) indexSoundRecording(tx *sql.Tx, ernID *cid.Cid, obj *meta.Object) error {
	graph := meta.NewGraph(i.store, obj)

	// Only *attempt* to load the ISRC, other IDs can be retrieved via GraphQL
	// Default to empty string if not present
	var isrc string
	v, err := graph.Get("SoundRecordingId", "ISRC", "@value")
	if err == nil {
		isrc = v.(string)
	}

	// Insert the DisplayArtist to party table
	srCid, err := obj.Get("SoundRecordingDetailsByTerritory")
	if err != nil {
		return err
	}
	var cids []*cid.Cid
	switch srCid := srCid.(type) {
	case *format.Link:
		cids = []*cid.Cid{srCid.Cid}
	case []interface{}:
		for _, x := range srCid {
			cid, ok := x.(*cid.Cid)
			if !ok {
				return fmt.Errorf("invalid resource type %T, expected *cid.Cid", x)
			}
			cids = append(cids, cid)
		}
	}
	for _, cid := range cids {
		obj, err := i.store.Get(cid)
		if err != nil {
			return err
		}
		_, err = i.insertParties(tx, obj, "DisplayArtist")
		if err != nil {
			return err
		}
	}

	// load the ReferenceTitle
	var title string
	rt, err := graph.Get("ReferenceTitle", "TitleText", "@value")
	if err == nil {
		title = rt.(string)
	} else if !meta.IsPathNotFound(err) {
		return err
	}

	// return an error if there is no ReferenceTitle, SoundRecordingId can be empty
	if title == "" {
		return fmt.Errorf("SoundRecording missing ReferenceTitle")
	}

	// update the sound_recording and resource_list indexes
	if _, err := tx.Exec(
		"INSERT INTO sound_recording (cid, id, title) VALUES ($1, $2, $3)",
		obj.Cid().String(), isrc, title,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(
		"INSERT INTO resource_list (ern_id, resource_id) VALUES ($1, $2)",
		ernID.String(), obj.Cid().String(),
	); err != nil {
		return err
	}

	return nil
}

// indexReleaseList indexes the ReleaseList for each Release composite
func (i *Indexer) indexReleaseList(tx *sql.Tx, ernID *cid.Cid, metaObj *meta.Object) error {
	// Much like the resource list, the release propoerty can be
	// a single release, or an array of links.
	rls, err := metaObj.Get("Release")
	if err != nil {
		return err
	}
	var cids []*cid.Cid
	switch rls := rls.(type) {
	case *format.Link:
		cids = []*cid.Cid{rls.Cid}
	case []interface{}:
		for _, x := range rls {
			id, ok := x.(*cid.Cid)
			if !ok {
				return fmt.Errorf("Invalid release type %T, expected *cid.Cid", x)
			}
			cids = append(cids, id)
		}
	}

	// load and index each Release link
	for _, id := range cids {
		rObj, err := i.store.Get(id)
		if err != nil {
			return err
		}
		if err := i.indexRelease(tx, ernID, rObj); err != nil {
			return err
		}
	}
	return nil
}

// indexRelease index each Release composite in the ReelaseList
func (i *Indexer) indexRelease(tx *sql.Tx, ernID *cid.Cid, metaObj *meta.Object) error {

	graph := meta.NewGraph(i.store, metaObj)

	// Only *attempt* to load the GRid, other IDs can be retrieved via GraphQL
	var grId string
	v, err := graph.Get("ReleaseId", "GRid", "@value")
	if err == nil {
		grId = v.(string)
	}

	// load the ReferenceTitle
	var title string
	rtl, err := graph.Get("ReferenceTitle", "TitleText", "@value")
	if err == nil {
		title = rtl.(string)
	} else if !meta.IsPathNotFound(err) {
		return err
	}

	// return an error if there is no ReferenceTitle, ReleaseId can be empty
	if title == "" {
		return fmt.Errorf("Release missing ReferenceTitle")
	}

	// update the release and release_list indexes
	_, err = tx.Exec(
		"INSERT INTO release (cid, id, title) VALUES ($1, $2, $3)",
		metaObj.Cid().String(), grId, title,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		"INSERT INTO release_list (ern_id, release_id) VALUES ($1, $2)",
		ernID.String(), metaObj.Cid().String(),
	)
	return err

}

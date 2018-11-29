package paychan

import (
	"github.com/iov-one/weave"
	"github.com/iov-one/weave/errors"
	"github.com/iov-one/weave/orm"
)

var _ orm.CloneableData = (*PaymentChannel)(nil)

// Validate ensures the payment channel is valid.
func (pc *PaymentChannel) Validate() error {
	if pc.Src == nil {
		return errors.E(CodeMissingCondition, "missing src")
	}
	if pc.SenderPubkey == nil {
		return errors.E(CodeMissingCondition, "missing sender pubkey")
	}
	if pc.Recipient == nil {
		return errors.E(CodeMissingCondition, "missing recipient")
	}
	if pc.Timeout <= 0 {
		return errors.E(CodeInvalidCondition, "timeout too small",
			errors.Labelf("timeout=%d", pc.Timeout))
	}
	if pc.Total == nil || !pc.Total.IsPositive() {
		return errors.E(CodeInvalidCondition, "non positive total",
			errors.Labelf("total=%s", pc.Total))
	}
	if len(pc.Memo) > 128 {
		return errors.E(CodeInvalidCondition, "memo too long")
	}

	// Transfer value must not be greater than the Total value represented
	// by the PaymentChannel.
	if pc.Transferred == nil || !pc.Transferred.IsNonNegative() || pc.Transferred.Compare(*pc.Total) > 0 {
		return errors.E(CodeInvalidCondition, "invalid transferred")
	}
	return nil
}

// Copy returns a shallow copy of this PaymentChannel.
func (pc PaymentChannel) Copy() orm.CloneableData {
	return &pc
}

// PaymentChannelBucket is a wrapper over orm.Bucket that ensures that only
// PaymentChannel entities can be persisted.
type PaymentChannelBucket struct {
	orm.Bucket
	idSeq orm.Sequence
}

// NewPaymentChannelBucket returns a bucket for storing PaymentChannel state.
func NewPaymentChannelBucket() PaymentChannelBucket {
	b := orm.NewBucket("paychan", orm.NewSimpleObj(nil, &PaymentChannel{}))
	return PaymentChannelBucket{
		Bucket: b,
		idSeq:  b.Sequence("id"),
	}
}

// Create adds given payment store entity to the store and returns the ID of
// the newly inserted entity.
func (b *PaymentChannelBucket) Create(db weave.KVStore, pc *PaymentChannel) (orm.Object, error) {
	key := b.idSeq.NextVal(db)
	obj := orm.NewSimpleObj(key, pc)
	return obj, b.Bucket.Save(db, obj)
}

// Save updates the state of given PaymentChannel entity in the store.
func (b *PaymentChannelBucket) Save(db weave.KVStore, obj orm.Object) error {
	if _, ok := obj.Value().(*PaymentChannel); !ok {
		return orm.ErrInvalidObject(obj.Value())
	}
	return b.Bucket.Save(db, obj)
}

// GetPaymentChannel returns a payment channel instance with given ID or
// returns an error.
func (b *PaymentChannelBucket) GetPaymentChannel(db weave.KVStore, paymentChannelID []byte) (*PaymentChannel, error) {
	obj, err := b.Get(db, paymentChannelID)
	if err != nil {
		return nil, err
	}
	if obj == nil || obj.Value() == nil {
		return nil, errors.E(CodeNotFound, "payment channel not found")
	}
	pc, ok := obj.Value().(*PaymentChannel)
	if !ok {
		return nil, errors.E(CodeNotFound, "payment channel not found")
	}
	return pc, nil
}

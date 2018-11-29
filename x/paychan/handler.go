package paychan

import (
	"github.com/iov-one/weave"
	"github.com/iov-one/weave/errors"
	"github.com/iov-one/weave/orm"
	"github.com/iov-one/weave/x"
	"github.com/iov-one/weave/x/cash"
	pkgerrors "github.com/pkg/errors"
)

const (
	createPaymentChannelCost   int64 = 300
	transferPaymentChannelCost int64 = 5
)

// RegisterQuery registers payment channel bucket under /paychans.
func RegisterQuery(qr weave.QueryRouter) {
	NewPaymentChannelBucket().Register("paychans", qr)
}

// RegisterRouters registers payment channel message handelers in given registry.
func RegisterRoutes(r weave.Registry, auth x.Authenticator, cash cash.Controller) {
	bucket := NewPaymentChannelBucket()
	r.Handle(pathCreatePaymentChannelMsg, &createPaymentChannelHandler{auth: auth, bucket: bucket, cash: cash})
	r.Handle(pathTransferPaymentChannelMsg, &transferPaymentChannelHandler{auth: auth, bucket: bucket, cash: cash})
	r.Handle(pathClosePaymentChannelMsg, &closePaymentChannelHandler{auth: auth, bucket: bucket, cash: cash})
}

type createPaymentChannelHandler struct {
	auth   x.Authenticator
	bucket PaymentChannelBucket
	cash   cash.Controller
}

var _ weave.Handler = (*createPaymentChannelHandler)(nil)

func (h *createPaymentChannelHandler) Check(ctx weave.Context, db weave.KVStore, tx weave.Tx) (weave.CheckResult, error) {
	var res weave.CheckResult
	if _, err := h.validate(ctx, db, tx); err != nil {
		return res, errors.E(err, "invalid message")
	}

	res.GasAllocated += createPaymentChannelCost
	return res, nil
}

func (h *createPaymentChannelHandler) validate(ctx weave.Context, db weave.KVStore, tx weave.Tx) (*CreatePaymentChannelMsg, error) {
	rmsg, err := tx.GetMsg()
	if err != nil {
		return nil, errors.E(err, "cannot get message")
	}
	msg, ok := rmsg.(*CreatePaymentChannelMsg)
	if !ok {
		// TODO
		//
		// ErrUnknownTxType is defined in different module, so for now
		// let's ignore that fact
		//
		// return nil, errors.ErrUnknownTxType(rmsg)
		//
		// this should be instead something like
		//
		// return nil, errors.E(errors.CodeUnknownTxType, errors.Labelf("tx=%T", rmsg))
		return nil, errors.E(CodeTODO, "unknown tx type", errors.Labelf("tx=%T", rmsg))
	}

	if err := msg.Validate(); err != nil {
		return msg, err
	}

	// Ensure that the timeout is in the future.
	if height, _ := weave.GetHeight(ctx); msg.Timeout <= height {
		return msg, errors.E(CodeInvalidCondition, "timeout in the past")
	}

	if !h.auth.HasAddress(ctx, msg.Src) {
		// TODO replace with
		//
		// return msg, errors.E(errors.CodeUnauthorized)
		return msg, errors.ErrUnauthorized()
	}

	return msg, nil
}

func (h *createPaymentChannelHandler) Deliver(ctx weave.Context, db weave.KVStore, tx weave.Tx) (weave.DeliverResult, error) {
	var res weave.DeliverResult
	msg, err := h.validate(ctx, db, tx)
	if err != nil {
		return res, errors.E(err, "invalid message")
	}

	obj, err := h.bucket.Create(db, &PaymentChannel{
		Src:          msg.Src,
		SenderPubkey: msg.SenderPubkey,
		Recipient:    msg.Recipient,
		Total:        msg.Total,
		Timeout:      msg.Timeout,
		Memo:         msg.Memo,
		Transferred:  &x.Coin{Ticker: msg.Total.Ticker},
	})
	if err != nil {
		return res, errors.E(err, "cannot create in bucket")
	}

	// Move coins from sender account and deposit total amount available on
	// that channels account.
	dst := paymentChannelAccount(obj.Key())
	if err := h.cash.MoveCoins(db, msg.Src, dst, *msg.Total); err != nil {
		return res, errors.E(err, "cannot move coins")
	}

	res.Data = obj.Key()
	return res, nil
}

type transferPaymentChannelHandler struct {
	auth   x.Authenticator
	bucket PaymentChannelBucket
	cash   cash.Controller
}

var _ weave.Handler = (*transferPaymentChannelHandler)(nil)

func (h *transferPaymentChannelHandler) Check(ctx weave.Context, db weave.KVStore, tx weave.Tx) (weave.CheckResult, error) {
	var res weave.CheckResult
	if _, err := h.validate(ctx, db, tx); err != nil {
		return res, errors.E(err, "invalid message")
	}
	res.GasAllocated += transferPaymentChannelCost
	return res, nil
}

func (h *transferPaymentChannelHandler) validate(ctx weave.Context, db weave.KVStore, tx weave.Tx) (*TransferPaymentChannelMsg, error) {
	rmsg, err := tx.GetMsg()
	if err != nil {
		return nil, errors.E(err, "cannot get message")
	}
	msg, ok := rmsg.(*TransferPaymentChannelMsg)
	if !ok {
		// TODO replace like before
		return nil, errors.ErrUnknownTxType(rmsg)
	}

	if err := msg.Validate(); err != nil {
		return msg, err
	}

	if weave.GetChainID(ctx) != msg.Payment.ChainId {
		return nil, ErrInvalidChainID(msg.Payment.ChainId)
	}

	pc, err := h.bucket.GetPaymentChannel(db, msg.Payment.ChannelId)
	if err != nil {
		return nil, err
	}

	// Check signature to ensure the message was not altered.
	raw, err := msg.Payment.Marshal()
	if err != nil {
		return nil, pkgerrors.Wrap(err, "serialize payment")
	}
	if !pc.SenderPubkey.Verify(raw, msg.Signature) {
		return msg, ErrInvalidSignature()
	}

	if !msg.Payment.Amount.SameType(*pc.Total) {
		return msg, ErrInvalidAmount(msg.Payment.Amount)
	}

	if msg.Payment.Amount.Compare(*pc.Total) > 0 {
		return msg, ErrInvalidAmount(msg.Payment.Amount)
	}
	// Payment is representing a cumulative amount that is to be
	// transferred to recipients account. Because it is cumulative, every
	// transfer request must be greater than the previous one.
	if msg.Payment.Amount.Compare(*pc.Transferred) <= 0 {
		return msg, ErrInvalidAmount(msg.Payment.Amount)
	}

	return msg, nil
}

func (h *transferPaymentChannelHandler) Deliver(ctx weave.Context, db weave.KVStore, tx weave.Tx) (weave.DeliverResult, error) {
	var res weave.DeliverResult
	msg, err := h.validate(ctx, db, tx)
	if err != nil {
		return res, err
	}

	pc, err := h.bucket.GetPaymentChannel(db, msg.Payment.ChannelId)
	if err != nil {
		return res, err
	}

	// Payment amount is total amount that should be transferred from
	// payment channel to recipient. Deduct already transferred funds and
	// move only the difference.
	diff, err := msg.Payment.Amount.Subtract(*pc.Transferred)
	if err != nil || diff.IsZero() {
		return res, ErrInvalidAmount(msg.Payment.Amount)
	}

	src := paymentChannelAccount(msg.Payment.ChannelId)
	if err := h.cash.MoveCoins(db, src, pc.Recipient, diff); err != nil {
		return res, err
	}

	// Track total amount transferred from the payment channel to the
	// recipients account.
	pc.Transferred = msg.Payment.Amount

	// We care about the latest memo only. Full history can be always
	// rebuild from the blockchain.
	pc.Memo = msg.Payment.Memo

	// If all funds were transferred, we can close the payment channel
	// because there is no further use for it. In addition, because all the
	// funds were used, no party is interested in closing it.
	//
	// To avoid "empty" payment channels in our database, delete it without
	// waiting for the explicit close request.
	if pc.Transferred.Equals(*pc.Total) {
		err := h.bucket.Delete(db, msg.Payment.ChannelId)
		return res, err
	}

	obj := orm.NewSimpleObj(msg.Payment.ChannelId, pc)
	err = h.bucket.Save(db, obj)
	return res, err
}

type closePaymentChannelHandler struct {
	auth   x.Authenticator
	bucket PaymentChannelBucket
	cash   cash.Controller
}

var _ weave.Handler = (*closePaymentChannelHandler)(nil)

func (h *closePaymentChannelHandler) Check(ctx weave.Context, db weave.KVStore, tx weave.Tx) (weave.CheckResult, error) {
	var res weave.CheckResult
	_, err := h.validate(ctx, db, tx)
	return res, err
}

func (h *closePaymentChannelHandler) Deliver(ctx weave.Context, db weave.KVStore, tx weave.Tx) (weave.DeliverResult, error) {
	var res weave.DeliverResult
	msg, err := h.validate(ctx, db, tx)
	if err != nil {
		return res, err
	}

	pc, err := h.bucket.GetPaymentChannel(db, msg.ChannelId)
	if err != nil {
		return res, err
	}

	// If payment channel funds were exhausted anyone is free to close it.
	if pc.Total.Equals(*pc.Transferred) {
		err := h.bucket.Delete(db, msg.ChannelId)
		return res, err
	}

	if height, _ := weave.GetHeight(ctx); pc.Timeout > height {
		// If timeout was not reached, only the recipient is allowed to
		// close the channel.
		if !h.auth.HasAddress(ctx, pc.Recipient) {
			return res, ErrNotAllowed("not recipient")
		}
	}

	// Before deleting the channel, return to sender all leftover funds
	// that are still allocated on this payment channel account.
	diff, err := pc.Total.Subtract(*pc.Transferred)
	if err != nil {
		return res, err
	}
	src := paymentChannelAccount(msg.ChannelId)
	if err := h.cash.MoveCoins(db, src, pc.Src, diff); err != nil {
		return res, err
	}
	err = h.bucket.Delete(db, msg.ChannelId)
	return res, err
}

func (h *closePaymentChannelHandler) validate(ctx weave.Context, db weave.KVStore, tx weave.Tx) (*ClosePaymentChannelMsg, error) {
	rmsg, err := tx.GetMsg()
	if err != nil {
		return nil, err
	}
	msg, ok := rmsg.(*ClosePaymentChannelMsg)
	if !ok {
		return nil, errors.ErrUnknownTxType(rmsg)
	}

	return msg, msg.Validate()
}

// paymentChannelAccount returns an account address for a payment channel with
// given ID.
// Each payment channel deposit an initial value from sender to ensure that it
// is available to the recipient upon request. Each payment channel has a
// unique account address that can be deducted from its ID.
func paymentChannelAccount(paymentChannelId []byte) weave.Address {
	return weave.NewCondition("paychan", "seq", paymentChannelId).Address()
}

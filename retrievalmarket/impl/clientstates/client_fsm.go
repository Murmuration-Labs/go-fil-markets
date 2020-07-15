package clientstates

import (
	"fmt"

	"github.com/filecoin-project/go-address"
	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-statemachine/fsm"
	"github.com/filecoin-project/specs-actors/actors/abi"
	"github.com/filecoin-project/specs-actors/actors/abi/big"
	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"

	rm "github.com/filecoin-project/go-fil-markets/retrievalmarket"
)

func recordReceived(deal *rm.ClientDealState, totalReceived uint64) error {
	deal.TotalReceived = totalReceived
	return nil
}

var paymentChannelCreationStates = []fsm.StateKey{
	rm.DealStatusWaitForAcceptance,
	rm.DealStatusAccepted,
	rm.DealStatusPaymentChannelCreating,
	rm.DealStatusPaymentChannelAddingFunds,
}

// ClientEvents are the events that can happen in a retrieval client
var ClientEvents = fsm.Events{
	fsm.Event(rm.ClientEventOpen).
		From(rm.DealStatusNew).ToNoChange(),

	// ProposeDeal handler events
	fsm.Event(rm.ClientEventWriteDealProposalErrored).
		FromAny().To(rm.DealStatusErrored).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = xerrors.Errorf("proposing deal: %w", err).Error()
			return nil
		}),
	fsm.Event(rm.ClientEventDealProposed).
		From(rm.DealStatusNew).To(rm.DealStatusWaitForAcceptance).
		Action(func(deal *rm.ClientDealState, channelID datatransfer.ChannelID) error {
			deal.ChannelID = channelID
			return nil
		}),

	// Initial deal acceptance events
	fsm.Event(rm.ClientEventDealRejected).
		From(rm.DealStatusWaitForAcceptance).To(rm.DealStatusRejected).
		Action(func(deal *rm.ClientDealState, message string) error {
			deal.Message = fmt.Sprintf("deal rejected: %s", message)
			return nil
		}),
	fsm.Event(rm.ClientEventDealNotFound).
		From(rm.DealStatusWaitForAcceptance).To(rm.DealStatusDealNotFound).
		Action(func(deal *rm.ClientDealState, message string) error {
			deal.Message = fmt.Sprintf("deal not found: %s", message)
			return nil
		}),
	fsm.Event(rm.ClientEventDealAccepted).
		From(rm.DealStatusWaitForAcceptance).To(rm.DealStatusAccepted),
	fsm.Event(rm.ClientEventUnknownResponseReceived).
		FromAny().To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, status rm.DealStatus) error {
			deal.Message = fmt.Sprintf("Unexpected deal response status: %s", rm.DealStatuses[status])
			return nil
		}),

	// Payment channel setup
	fsm.Event(rm.ClientEventPaymentChannelErrored).
		FromMany(rm.DealStatusAccepted, rm.DealStatusPaymentChannelCreating).To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = xerrors.Errorf("get or create payment channel: %w", err).Error()
			return nil
		}),
	fsm.Event(rm.ClientEventPaymentChannelCreateInitiated).
		From(rm.DealStatusAccepted).To(rm.DealStatusPaymentChannelCreating).
		Action(func(deal *rm.ClientDealState, msgCID cid.Cid) error {
			deal.WaitMsgCID = &msgCID
			return nil
		}),
	fsm.Event(rm.ClientEventPaymentChannelAddingFunds).
		FromMany(rm.DealStatusAccepted).To(rm.DealStatusPaymentChannelAddingFunds).
		Action(func(deal *rm.ClientDealState, msgCID cid.Cid, payCh address.Address) error {
			deal.WaitMsgCID = &msgCID
			deal.PaymentInfo = &rm.PaymentInfo{
				PayCh: payCh,
			}
			return nil
		}),
	fsm.Event(rm.ClientEventPaymentChannelReady).
		FromMany(rm.DealStatusPaymentChannelCreating, rm.DealStatusPaymentChannelAddingFunds).
		To(rm.DealStatusOngoing).
		Action(func(deal *rm.ClientDealState, payCh address.Address, lane uint64) error {
			deal.PaymentInfo = &rm.PaymentInfo{
				PayCh: payCh,
				Lane:  lane,
			}
			return nil
		}),
	fsm.Event(rm.ClientEventAllocateLaneErrored).
		FromMany(rm.DealStatusPaymentChannelCreating, rm.DealStatusPaymentChannelAddingFunds).
		To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = xerrors.Errorf("allocating payment lane: %w", err).Error()
			return nil
		}),
	fsm.Event(rm.ClientEventPaymentChannelAddFundsErrored).
		From(rm.DealStatusPaymentChannelAddingFunds).To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = xerrors.Errorf("wait for add funds: %w", err).Error()
			return nil
		}),

	// Transfer Channel Errors
	fsm.Event(rm.ClientEventDataTransferError).
		FromAny().To(rm.DealStatusErrored).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = fmt.Sprintf("error generated by data transfer: %s", err.Error())
			return nil
		}),

	// Receiving requests for payment
	fsm.Event(rm.ClientEventLastPaymentRequested).
		FromMany(
			rm.DealStatusOngoing,
			rm.DealStatusFundsNeededLastPayment,
			rm.DealStatusFundsNeeded).To(rm.DealStatusFundsNeededLastPayment).
		From(rm.DealStatusBlocksComplete).To(rm.DealStatusSendFundsLastPayment).
		FromMany(
			paymentChannelCreationStates...).ToJustRecord().
		Action(func(deal *rm.ClientDealState, paymentOwed abi.TokenAmount) error {
			deal.PaymentRequested = big.Add(deal.PaymentRequested, paymentOwed)
			deal.LastPaymentRequested = true
			return nil
		}),
	fsm.Event(rm.ClientEventPaymentRequested).
		FromMany(
			rm.DealStatusOngoing,
			rm.DealStatusBlocksComplete,
			rm.DealStatusFundsNeeded).To(rm.DealStatusFundsNeeded).
		FromMany(
			paymentChannelCreationStates...).ToJustRecord().
		Action(func(deal *rm.ClientDealState, paymentOwed abi.TokenAmount) error {
			deal.PaymentRequested = big.Add(deal.PaymentRequested, paymentOwed)
			return nil
		}),

	// Receiving data
	fsm.Event(rm.ClientEventAllBlocksReceived).
		FromMany(
			rm.DealStatusOngoing,
			rm.DealStatusBlocksComplete,
		).To(rm.DealStatusBlocksComplete).
		FromMany(paymentChannelCreationStates...).ToJustRecord().
		From(rm.DealStatusFundsNeededLastPayment).To(rm.DealStatusSendFundsLastPayment).
		Action(func(deal *rm.ClientDealState) error {
			deal.AllBlocksReceived = true
			return nil
		}),
	fsm.Event(rm.ClientEventBlocksReceived).
		FromMany(rm.DealStatusOngoing,
			rm.DealStatusFundsNeeded,
			rm.DealStatusFundsNeededLastPayment).ToNoChange().
		FromMany(paymentChannelCreationStates...).ToJustRecord().
		Action(recordReceived),

	fsm.Event(rm.ClientEventSendFunds).
		From(rm.DealStatusFundsNeeded).To(rm.DealStatusSendFunds).
		From(rm.DealStatusFundsNeededLastPayment).To(rm.DealStatusSendFundsLastPayment),

	// Sending Payments
	fsm.Event(rm.ClientEventFundsExpended).
		FromMany(rm.DealStatusSendFunds, rm.DealStatusSendFundsLastPayment).To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, expectedTotal string, actualTotal string) error {
			deal.Message = fmt.Sprintf("not enough funds left: expected amt = %s, actual amt = %s", expectedTotal, actualTotal)
			return nil
		}),
	fsm.Event(rm.ClientEventBadPaymentRequested).
		FromMany(rm.DealStatusSendFunds, rm.DealStatusSendFundsLastPayment).To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, message string) error {
			deal.Message = message
			return nil
		}),
	fsm.Event(rm.ClientEventCreateVoucherFailed).
		FromMany(rm.DealStatusSendFunds, rm.DealStatusSendFundsLastPayment).To(rm.DealStatusFailing).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = xerrors.Errorf("creating payment voucher: %w", err).Error()
			return nil
		}),
	fsm.Event(rm.ClientEventWriteDealPaymentErrored).
		FromAny().To(rm.DealStatusErrored).
		Action(func(deal *rm.ClientDealState, err error) error {
			deal.Message = xerrors.Errorf("writing deal payment: %w", err).Error()
			return nil
		}),
	fsm.Event(rm.ClientEventPaymentSent).
		From(rm.DealStatusSendFunds).To(rm.DealStatusOngoing).
		From(rm.DealStatusSendFundsLastPayment).To(rm.DealStatusFinalizing).
		Action(func(deal *rm.ClientDealState) error {
			// paymentRequested = 0
			// fundsSpent = fundsSpent + paymentRequested
			// if paymentRequested / pricePerByte >= currentInterval
			// currentInterval = currentInterval + proposal.intervalIncrease
			// bytesPaidFor = bytesPaidFor + (paymentRequested / pricePerByte)
			deal.FundsSpent = big.Add(deal.FundsSpent, deal.PaymentRequested)
			bytesPaidFor := big.Div(deal.PaymentRequested, deal.PricePerByte).Uint64()
			if bytesPaidFor >= deal.CurrentInterval {
				deal.CurrentInterval += deal.DealProposal.PaymentIntervalIncrease
			}
			deal.BytesPaidFor += bytesPaidFor
			deal.PaymentRequested = abi.NewTokenAmount(0)
			return nil
		}),

	fsm.Event(rm.ClientEventComplete).
		FromMany(rm.DealStatusFinalizing).To(rm.DealStatusCompleted),
	fsm.Event(rm.ClientEventCancelComplete).
		From(rm.DealStatusFailing).To(rm.DealStatusErrored),
}

// ClientFinalityStates are terminal states after which no further events are received
var ClientFinalityStates = []fsm.StateKey{
	rm.DealStatusErrored,
	rm.DealStatusCompleted,
}

// ClientStateEntryFuncs are the handlers for different states in a retrieval client
var ClientStateEntryFuncs = fsm.StateEntryFuncs{
	rm.DealStatusNew:                       ProposeDeal,
	rm.DealStatusAccepted:                  SetupPaymentChannelStart,
	rm.DealStatusPaymentChannelCreating:    WaitForPaymentChannelCreate,
	rm.DealStatusPaymentChannelAddingFunds: WaitForPaymentChannelAddFunds,
	rm.DealStatusOngoing:                   Ongoing,
	rm.DealStatusFundsNeeded:               ProcessPaymentRequested,
	rm.DealStatusFundsNeededLastPayment:    ProcessPaymentRequested,
	rm.DealStatusSendFunds:                 SendFunds,
	rm.DealStatusSendFundsLastPayment:      SendFunds,
	rm.DealStatusFailing:                   CancelDeal,
}

package requestvalidation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-multistore"
	"github.com/filecoin-project/go-state-types/abi"

	"github.com/filecoin-project/go-fil-markets/piecestore"
	"github.com/filecoin-project/go-fil-markets/retrievalmarket"
	"github.com/filecoin-project/go-fil-markets/retrievalmarket/impl/requestvalidation"
	"github.com/filecoin-project/go-fil-markets/shared"
	"github.com/filecoin-project/go-fil-markets/shared_testutil"
)

func TestValidatePush(t *testing.T) {
	fve := &fakeValidationEnvironment{}
	sender := shared_testutil.GeneratePeers(1)[0]
	voucher := shared_testutil.MakeTestDealProposal()
	requestValidator := requestvalidation.NewProviderRequestValidator(fve)
	voucherResult, err := requestValidator.ValidatePush(sender, &voucher, voucher.PayloadCID, shared.AllSelector())
	require.Equal(t, nil, voucherResult)
	require.Error(t, err)
}

func TestValidatePull(t *testing.T) {
	proposal := shared_testutil.MakeTestDealProposal()
	testCases := map[string]struct {
		fve                   fakeValidationEnvironment
		sender                peer.ID
		voucher               datatransfer.Voucher
		baseCid               cid.Cid
		selector              ipld.Node
		expectedVoucherResult datatransfer.VoucherResult
		expectedError         error
	}{
		"not a retrieval voucher": {
			expectedError: errors.New("wrong voucher type"),
		},
		"proposal and base cid do not match": {
			baseCid:       shared_testutil.GenerateCids(1)[0],
			voucher:       &proposal,
			expectedError: errors.New("incorrect CID for this proposal"),
		},
		"proposal and selector do not match": {
			baseCid:       proposal.PayloadCID,
			selector:      builder.NewSelectorSpecBuilder(basicnode.Style.Any).Matcher().Node(),
			voucher:       &proposal,
			expectedError: errors.New("incorrect selector for this proposal"),
		},
		"get piece other err": {
			fve: fakeValidationEnvironment{
				GetPieceErr: errors.New("something went wrong"),
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: errors.New("something went wrong"),
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status:  retrievalmarket.DealStatusErrored,
				ID:      proposal.ID,
				Message: "something went wrong",
			},
		},
		"get piece not found err": {
			fve: fakeValidationEnvironment{
				GetPieceErr: retrievalmarket.ErrNotFound,
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: retrievalmarket.ErrNotFound,
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status:  retrievalmarket.DealStatusDealNotFound,
				ID:      proposal.ID,
				Message: retrievalmarket.ErrNotFound.Error(),
			},
		},
		"check deal params err": {
			fve: fakeValidationEnvironment{
				CheckDealParamsError: errors.New("something went wrong"),
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: errors.New("something went wrong"),
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status:  retrievalmarket.DealStatusRejected,
				ID:      proposal.ID,
				Message: "something went wrong",
			},
		},
		"run deal decioning error": {
			fve: fakeValidationEnvironment{
				RunDealDecisioningLogicError: errors.New("something went wrong"),
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: errors.New("something went wrong"),
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status:  retrievalmarket.DealStatusErrored,
				ID:      proposal.ID,
				Message: "something went wrong",
			},
		},
		"run deal decioning rejected": {
			fve: fakeValidationEnvironment{
				RunDealDecisioningLogicFailReason: "something went wrong",
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: errors.New("something went wrong"),
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status:  retrievalmarket.DealStatusRejected,
				ID:      proposal.ID,
				Message: "something went wrong",
			},
		},
		"store ID error": {
			fve: fakeValidationEnvironment{
				RunDealDecisioningLogicAccepted: true,
				NextStoreIDError:                errors.New("something went wrong"),
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: errors.New("something went wrong"),
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status:  retrievalmarket.DealStatusErrored,
				ID:      proposal.ID,
				Message: "something went wrong",
			},
		},
		"begin tracking error": {
			fve: fakeValidationEnvironment{
				BeginTrackingError:              errors.New("everything is awful"),
				RunDealDecisioningLogicAccepted: true,
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: errors.New("everything is awful"),
		},
		"success": {
			fve: fakeValidationEnvironment{
				RunDealDecisioningLogicAccepted: true,
			},
			baseCid:       proposal.PayloadCID,
			selector:      shared.AllSelector(),
			voucher:       &proposal,
			expectedError: datatransfer.ErrPause,
			expectedVoucherResult: &retrievalmarket.DealResponse{
				Status: retrievalmarket.DealStatusAccepted,
				ID:     proposal.ID,
			},
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			requestValidator := requestvalidation.NewProviderRequestValidator(&data.fve)
			voucherResult, err := requestValidator.ValidatePull(data.sender, data.voucher, data.baseCid, data.selector)
			require.Equal(t, data.expectedVoucherResult, voucherResult)
			if data.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.EqualError(t, err, data.expectedError.Error())
			}
		})
	}
}

type fakeValidationEnvironment struct {
	PieceInfo                         piecestore.PieceInfo
	GetPieceErr                       error
	CheckDealParamsError              error
	RunDealDecisioningLogicAccepted   bool
	RunDealDecisioningLogicFailReason string
	RunDealDecisioningLogicError      error
	BeginTrackingError                error
	NextStoreIDValue                  multistore.StoreID
	NextStoreIDError                  error
}

func (fve *fakeValidationEnvironment) GetPiece(c cid.Cid, pieceCID *cid.Cid) (piecestore.PieceInfo, error) {
	return fve.PieceInfo, fve.GetPieceErr
}

// CheckDealParams verifies the given deal params are acceptable
func (fve *fakeValidationEnvironment) CheckDealParams(pricePerByte abi.TokenAmount, paymentInterval uint64, paymentIntervalIncrease uint64, unsealPrice abi.TokenAmount) error {
	return fve.CheckDealParamsError
}

// RunDealDecisioningLogic runs custom deal decision logic to decide if a deal is accepted, if present
func (fve *fakeValidationEnvironment) RunDealDecisioningLogic(ctx context.Context, state retrievalmarket.ProviderDealState) (bool, string, error) {
	return fve.RunDealDecisioningLogicAccepted, fve.RunDealDecisioningLogicFailReason, fve.RunDealDecisioningLogicError
}

// StateMachines returns the FSM Group to begin tracking with
func (fve *fakeValidationEnvironment) BeginTracking(pds retrievalmarket.ProviderDealState) error {
	return fve.BeginTrackingError
}

func (fve *fakeValidationEnvironment) NextStoreID() (multistore.StoreID, error) {
	return fve.NextStoreIDValue, fve.NextStoreIDError
}

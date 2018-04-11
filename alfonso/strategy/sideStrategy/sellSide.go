package sideStrategy

import (
	"math"

	"github.com/lightyeario/kelp/alfonso/strategy/level"

	"github.com/lightyeario/kelp/support/exchange/number"

	"github.com/lightyeario/kelp/alfonso/priceFeed"
	kelp "github.com/lightyeario/kelp/support"
	"github.com/stellar/go/build"
	"github.com/stellar/go/clients/horizon"
	"github.com/stellar/go/support/log"
)

// SellSideStrategy is a strategy to sell a specific currency on SDEX on a single side by reading prices from an exchange
type SellSideStrategy struct {
	txButler            *kelp.TxButler
	assetBase           *horizon.Asset
	assetQuote          *horizon.Asset
	pf                  priceFeed.FeedPair
	levelsProvider      level.Provider
	priceTolerance      float64
	amountTolerance     float64
	divideAmountByPrice bool

	// uninitialized
	centerPrice   float64
	currentLevels []level.Level // levels for current iteration
	maxAssetBase  float64
	maxAssetQuote float64
}

// ensure it implements SideStrategy
var _ SideStrategy = &SellSideStrategy{}

// MakeSellSideStrategy is a factory method for SellSideStrategy
func MakeSellSideStrategy(
	txButler *kelp.TxButler,
	assetBase *horizon.Asset,
	assetQuote *horizon.Asset,
	pf priceFeed.FeedPair,
	levelsProvider level.Provider,
	priceTolerance float64,
	amountTolerance float64,
	divideAmountByPrice bool,
) SideStrategy {
	return &SellSideStrategy{
		txButler:            txButler,
		assetBase:           assetBase,
		assetQuote:          assetQuote,
		pf:                  pf,
		levelsProvider:      levelsProvider,
		priceTolerance:      priceTolerance,
		amountTolerance:     amountTolerance,
		divideAmountByPrice: divideAmountByPrice,
	}
}

// PruneExistingOffers impl
func (s *SellSideStrategy) PruneExistingOffers(offers []horizon.Offer) ([]build.TransactionMutator, []horizon.Offer) {
	pruneOps := []build.TransactionMutator{}
	for i := len(s.currentLevels); i < len(offers); i++ {
		pOp := s.txButler.DeleteOffer(offers[i])
		pruneOps = append(pruneOps, &pOp)
	}
	if len(offers) > len(s.currentLevels) {
		offers = offers[:len(s.currentLevels)]
	}
	return pruneOps, offers
}

// PreUpdate impl
func (s *SellSideStrategy) PreUpdate(maxAssetBase float64, maxAssetQuote float64) error {
	s.maxAssetBase = maxAssetBase
	s.maxAssetQuote = maxAssetQuote

	var e error
	s.centerPrice, e = s.pf.GetCenterPrice()
	if e != nil {
		log.Error("Center price couldn't be loaded! ", e)
		return e
	} else {
		log.Info("Center price: ", s.centerPrice, "        v0.2")
	}

	// load currentLevels only once here
	s.currentLevels, e = s.levelsProvider.GetLevels(s.centerPrice)
	if e != nil {
		log.Error("Center price couldn't be loaded! ", e)
		return e
	}
	return nil
}

// UpdateWithOps impl
func (s *SellSideStrategy) UpdateWithOps(offers []horizon.Offer) (ops []build.TransactionMutator, newTopOffer *number.Number, e error) {
	newTopOffer = nil
	for i := len(s.currentLevels) - 1; i >= 0; i-- {
		op := s.updateSellLevel(offers, i)
		if op != nil {
			offer, e := number.FromString(op.MO.Price.String(), 7)
			if e != nil {
				return nil, nil, e
			}

			// newTopOffer is minOffer because this is a sell strategy, and the lowest price is the best (top) price on the orderbook
			if newTopOffer == nil || offer.AsFloat() < newTopOffer.AsFloat() {
				newTopOffer = offer
			}

			ops = append(ops, op)
		}
	}
	return ops, newTopOffer, nil
}

// PostUpdate impl
func (s *SellSideStrategy) PostUpdate() error {
	return nil
}

// Selling Base
func (s *SellSideStrategy) updateSellLevel(offers []horizon.Offer, index int) *build.ManageOfferBuilder {
	targetPrice := s.currentLevels[index].TargetPrice()
	targetAmount := s.currentLevels[index].TargetAmount()
	if s.divideAmountByPrice {
		targetAmount /= targetPrice
	}
	targetAmount = math.Min(targetAmount, s.maxAssetBase)

	if len(offers) <= index {
		// no existing offer at this index
		log.Info("create sell: target:", targetPrice, " ta:", targetAmount)
		return s.txButler.CreateSellOffer(*s.assetBase, *s.assetQuote, targetPrice, targetAmount)
	}

	highestPrice := targetPrice + targetPrice*s.priceTolerance
	lowestPrice := targetPrice - targetPrice*s.priceTolerance
	minAmount := targetAmount - targetAmount*s.amountTolerance
	maxAmount := targetAmount + targetAmount*s.amountTolerance

	//check if existing offer needs to be modified
	curPrice := kelp.GetPrice(offers[index])
	curAmount := kelp.AmountStringAsFloat(offers[index].Amount)

	// existing offer not within tolerances
	priceTrigger := (curPrice > highestPrice) || (curPrice < lowestPrice)
	amountTrigger := (curAmount < minAmount) || (curAmount > maxAmount)
	if priceTrigger || amountTrigger {
		log.Info("mod sell curPrice: ", curPrice, ", highPrice: ", highestPrice, ", lowPrice: ", lowestPrice, ", curAmt: ", curAmount, ", minAmt: ", minAmount, ", maxAmt: ", maxAmount)
		return s.txButler.ModifySellOffer(offers[index], targetPrice, targetAmount)
	}
	return nil
}
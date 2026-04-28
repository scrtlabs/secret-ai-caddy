package x402

import (
	"go.uber.org/zap"
)

// SettlementEngineImpl finalizes reservations with actual usage.
type SettlementEngineImpl struct {
	ledger      *LedgerImpl
	quoteEngine *QuoteEngineImpl
	logger      *zap.Logger
}

// NewSettlementEngine creates a new SettlementEngine.
func NewSettlementEngine(ledger *LedgerImpl, quoteEngine *QuoteEngineImpl, logger *zap.Logger) *SettlementEngineImpl {
	return &SettlementEngineImpl{
		ledger:      ledger,
		quoteEngine: quoteEngine,
		logger:      logger,
	}
}

// Settle finalizes a reservation with actual usage.
func (s *SettlementEngineImpl) Settle(reservationID string, inputTokens, outputTokens int, model string) (*SettlementResult, error) {
	// Look up the reservation to get estimated cost
	agent, reservation := s.ledger.findReservation(reservationID)
	if agent == nil || reservation == nil {
		return nil, ErrReservationNotFound
	}

	// Reprice using actual output tokens (not the budget estimate)
	actualQuote, err := s.quoteEngine.Estimate(model, inputTokens, outputTokens)
	if err != nil {
		// On pricing error, commit the full reserved amount
		s.logger.Error("Settlement repricing failed, charging full reservation",
			zap.String("reservation_id", reservationID),
			zap.Error(err))
		if commitErr := s.ledger.Commit(reservationID, reservation.Amount); commitErr != nil {
			return nil, commitErr
		}
		return &SettlementResult{
			ReservationID: reservationID,
			AgentAddress:  reservation.AgentAddress,
			EstimatedCost: reservation.Amount,
			ActualCost:    reservation.Amount,
			Refunded:      0,
			InputTokens:   inputTokens,
			OutputTokens:  outputTokens,
			Model:         model,
		}, nil
	}

	actualCost := actualQuote.TotalCost
	// Don't charge more than reserved
	if actualCost > reservation.Amount {
		actualCost = reservation.Amount
	}

	if err := s.ledger.Commit(reservationID, actualCost); err != nil {
		return nil, err
	}

	result := &SettlementResult{
		ReservationID: reservationID,
		AgentAddress:  reservation.AgentAddress,
		EstimatedCost: reservation.Amount,
		ActualCost:    actualCost,
		Refunded:      reservation.Amount - actualCost,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
		Model:         model,
	}

	s.logger.Info("Settlement completed",
		zap.String("reservation_id", reservationID),
		zap.String("agent", reservation.AgentAddress),
		zap.Int64("estimated", reservation.Amount),
		zap.Int64("actual", actualCost),
		zap.Int64("refunded", result.Refunded),
		zap.Int("input_tokens", inputTokens),
		zap.Int("output_tokens", outputTokens),
		zap.String("model", model))

	return result, nil
}

// Cancel releases a reservation without charging.
func (s *SettlementEngineImpl) Cancel(reservationID string) error {
	err := s.ledger.Release(reservationID)
	if err != nil {
		s.logger.Error("Failed to cancel reservation",
			zap.String("reservation_id", reservationID),
			zap.Error(err))
		return err
	}
	s.logger.Debug("Reservation cancelled",
		zap.String("reservation_id", reservationID))
	return nil
}

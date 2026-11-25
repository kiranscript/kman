package alerts

// PriorityTransactions is how many transactions the priority lane has opened.
func (s *Service) PriorityTransactions() int64 { return s.priorityTxs.Load() }

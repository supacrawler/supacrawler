package robots

type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) IsAllowed(_ string, _ string) bool { return true }

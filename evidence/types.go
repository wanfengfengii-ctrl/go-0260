package evidence

// EvidenceKind enumerates the kinds of evidence versions produced.
type EvidenceKind string

const (
	EvidenceTurnCoverage EvidenceKind = "turn_coverage"
	EvidenceCutScore     EvidenceKind = "cut_score"
	EvidenceToxin        EvidenceKind = "toxin"
	EvidenceChemistry    EvidenceKind = "chemistry"
	EvidenceRejudgement  EvidenceKind = "rejudgement"
	EvidenceReview       EvidenceKind = "review"
)

// AdapterKind enumerates the external instruments that may be scripted to fail.
type AdapterKind string

const (
	AdapterProbe         AdapterKind = "probe"
	AdapterToxinReader   AdapterKind = "toxin_reader"
	AdapterMoistureMeter AdapterKind = "moisture_meter"
)

// AdapterStatus is the outcome of a single instrument attempt.
type AdapterStatus string

const (
	AdapterPending      AdapterStatus = "pending"
	AdapterRejected     AdapterStatus = "rejected"
	AdapterDisconnected AdapterStatus = "disconnected"
	AdapterTimeout      AdapterStatus = "timeout"
	AdapterMalformed    AdapterStatus = "malformed"
	AdapterSucceeded    AdapterStatus = "succeeded"
)

// ReviewKind enumerates review types.
type ReviewKind string

const (
	ReviewIndependent ReviewKind = "independent"
)

// Decision is a review or finalize decision.
type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
)

// TurnCoverageCell records one turn-node temperature coverage reading.
type TurnCoverageCell struct {
	TaskID            string
	Generation        int64
	BinID             string
	TurnNode          int
	TemperatureCentiC int64
	DurationMinutes   int64
	TurnCount         int64
	GapFillFlag       bool
	SlopeMilliPerMin  int64
	Valid             bool
}

// BlindSample is a coded sample whose bin mapping may be revealed only once.
// AssignedBinID is the hidden origin bin frozen at lock time; it is surfaced as
// RevealedBinID only once the reveal gate opens, then the sample is sealed.
type BlindSample struct {
	BlindCode        string
	TaskID           string
	Generation       int64
	SampleSize       int64
	AssignedBinID    string
	RevealedBinID    string
	RevealTick       int64
	RevealGeneration int64
	Sealed           bool
}

// EvidenceVersion is an immutable, append-only evidence record.
type EvidenceVersion struct {
	EvidenceID    string
	TaskID        string
	Generation    int64
	EvidenceKind  EvidenceKind
	SubjectKey    string
	VersionNo     int64
	PayloadHash   string
	IntegerValues []int64
	Accepted      bool
	RejectCode    string
	CreatedAtTick int64
}

// AdapterAttempt is one auditable instrument call, including retries.
type AdapterAttempt struct {
	AttemptID       string
	TaskID          string
	Generation      int64
	AdapterKind     AdapterKind
	TargetKey       string
	ScriptStep      int
	LogicalTick     int64
	Status          AdapterStatus
	StableErrorCode string
	RawDigest       string
	RetryAfterTick  int64
}

// ReviewRecord is one independent review decision.
type ReviewRecord struct {
	TaskID        string
	Generation    int64
	ReviewerID    string
	ReviewKind    ReviewKind
	Decision      Decision
	ReasonCode    string
	CreatedAtTick int64
}

// FinalCredential is the single terminal credential issued for a task.
type FinalCredential struct {
	TaskID             string
	Generation         int64
	CredentialID       string
	TerminalState      string
	WinnerOperationKey string
	IssuedAtTick       int64
	LeasedWindowID     string
	Digest             string
}

package params

const (
	// JRM-Alastria Gas Limit
	// In a Public/Permissioned network like Alastria we do not want dynamic gas limits and also they have to be reasonable.
	// The hardcoded MinGasLimit of 700M exposes the network to DoS attacks.
	// In our case, 8M is a limit big enough for the requirements of the network, and protects its members from ill-behaved
	// applications.
	// For the moment this is hardcoded, but it will be configurable in the future.
	// The Alastria gas limit applies starting with the block number 106983273.
	AlastriaGasLimit            uint64 = 30000000
	AlastriaGasLimitBlockNumber uint64 = 106983273

	// The time between blocks in the RedT network
	AlastriaBlockPeriod uint64 = 3
)

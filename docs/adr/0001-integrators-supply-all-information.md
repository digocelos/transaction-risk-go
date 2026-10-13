# Integrators supply all information; the toolkit performs no lookups

The toolkit scores only what it receives. It does not query DICT, data providers, customer history, device stores, or limit systems. Integrators obtain that information, supply it through the input contract, and the toolkit validates it. This keeps the toolkit free of network dependencies and credentials, makes every score reproducible from its input alone, and lets the same contract serve both live scoring and training.

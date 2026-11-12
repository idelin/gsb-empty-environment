package netsec

// Shape definitions mirror the subset of the Stellar XDR contract that can
// appear inside a transaction. The verifier only needs to skip these bytes;
// an unrecognised discriminant aborts parsing rather than being accepted.

func init() {
	registerStellarShapes()
}

func registerStellarShapes() {
	// MuxedAccount union:
	//   MUXED_ED25519(0) -> AccountID
	//   MUXED_ED25519_MUXED(1) -> { ed25519 uint256, id uint64 }
	registerShape("MuxedAccount", unionShape{arms: map[uint32]shape{
		0: refShape{"AccountID"},
		1: structShape{fields: []shape{shOpaque32, shU64}},
	}})

	// AccountID is itself a union with one key type: PUBLIC_KEY_TYPE_ED25519(0).
	registerShape("AccountID", unionShape{arms: map[uint32]shape{
		0: shOpaque32,
	}})

	// Memo: MEMO_NONE(0)=void, MEMO_TEXT(1)=string<28>, MEMO_ID(2)=uint64,
	// MEMO_HASH(3)=hash, MEMO_RETURN(4)=hash.
	registerShape("Memo", unionShape{arms: map[uint32]shape{
		0: nil,
		1: shString28,
		2: shU64,
		3: shOpaque32,
		4: shOpaque32,
	}})

	registerShape("TimeBounds", structShape{fields: []shape{shU64, shU64}})
	registerShape("LedgerBounds", structShape{fields: []shape{shU32, shU32}})

	// SignerKey union.
	registerShape("SignerKey", unionShape{arms: map[uint32]shape{
		0: shOpaque32,                                            // SIGNER_KEY_TYPE_ED25519
		1: shOpaque32,                                            // SIGNER_KEY_TYPE_PRE_AUTH_TX
		2: shOpaque32,                                            // SIGNER_KEY_TYPE_HASH_X
		3: structShape{fields: []shape{shOpaque32, shDataValue}}, // ED25519_SIGNED_PAYLOAD
	}})

	registerShape("Signer", structShape{fields: []shape{
		refShape{"SignerKey"}, shU32,
	}})

	// Preconditions: NONE(0)=void, TIME(1)=TimeBounds, V2(2)=PreconditionsV2.
	registerShape("PreconditionsV2", structShape{fields: []shape{
		optionalShape{refShape{"TimeBounds"}},
		optionalShape{refShape{"LedgerBounds"}},
		optionalShape{shU32},
		varArrayShape{max: 2, inner: refShape{"SignerKey"}},
	}})
	registerShape("Preconditions", unionShape{arms: map[uint32]shape{
		0: nil,
		1: refShape{"TimeBounds"},
		2: refShape{"PreconditionsV2"},
	}})

	// Asset unions used by payment-like operations.
	registerShape("Asset", unionShape{arms: map[uint32]shape{
		0: nil,
		1: structShape{fields: []shape{fixedOpaqueShape{n: 4}, refShape{"AccountID"}}},
		2: structShape{fields: []shape{fixedOpaqueShape{n: 12}, refShape{"AccountID"}}},
		3: shOpaque32, // POOL_SHARE: liquidity pool id
	}})
	// ChangeTrustAsset adds the constant-product pool parameters on arm 3.
	registerShape("ChangeTrustAsset", unionShape{arms: map[uint32]shape{
		0: nil,
		1: structShape{fields: []shape{fixedOpaqueShape{n: 4}, refShape{"AccountID"}}},
		2: structShape{fields: []shape{fixedOpaqueShape{n: 12}, refShape{"AccountID"}}},
		3: unionShape{arms: map[uint32]shape{
			0: structShape{fields: []shape{
				refShape{"Asset"}, refShape{"Asset"}, shI32,
			}},
		}},
	}})

	registerShape("Price", structShape{fields: []shape{shI32, shI32}})

	// ClaimPredicate / Claimant (recursive).
	registerShape("ClaimPredicate", unionShape{arms: map[uint32]shape{
		0: nil, // UNCONDITIONAL
		1: structShape{fields: []shape{ // AND
			varArrayShape{max: 100, inner: refShape{"ClaimPredicate"}},
		}},
		2: structShape{fields: []shape{ // OR
			varArrayShape{max: 100, inner: refShape{"ClaimPredicate"}},
		}},
		3: refShape{"ClaimPredicate"}, // NOT
		4: shI64,                      // BEFORE_RELATIVE_TIME
		5: shI64,                      // BEFORE_ABSOLUTE_TIME
	}})
	registerShape("Claimant", unionShape{arms: map[uint32]shape{
		0: structShape{fields: []shape{refShape{"AccountID"}, refShape{"ClaimPredicate"}}},
	}})

	// Operation bodies; enum values follow the Stellar XDR OperationType.
	registerShape("CreateAccountOp", structShape{fields: []shape{
		refShape{"AccountID"}, shI64,
	}})
	registerShape("PaymentOp", structShape{fields: []shape{
		refShape{"MuxedAccount"}, refShape{"Asset"}, shI64,
	}})
	registerShape("PathPaymentStrictReceiveOp", structShape{fields: []shape{
		refShape{"Asset"}, refShape{"MuxedAccount"}, shI64, refShape{"Asset"}, shI64,
		varArrayShape{max: 5, inner: refShape{"Asset"}},
	}})
	registerShape("ManageSellOfferOp", structShape{fields: []shape{
		refShape{"Asset"}, refShape{"Asset"}, shI64, refShape{"Price"}, shU32,
	}})
	registerShape("CreatePassiveSellOfferOp", structShape{fields: []shape{
		refShape{"Asset"}, refShape{"Asset"}, shI64, refShape{"Price"},
	}})
	registerShape("SetOptionsOp", structShape{fields: []shape{
		optionalShape{refShape{"AccountID"}},
		optionalShape{shU32},
		optionalShape{shU32},
		optionalShape{shU32},
		optionalShape{shU32},
		optionalShape{shU32},
		optionalShape{shU32},
		optionalShape{refShape{"HomeDomain"}},
		optionalShape{refShape{"Signer"}},
	}})
	registerShape("HomeDomain", primShape{kind: primString, max: 32})
	registerShape("ChangeTrustOp", structShape{fields: []shape{
		refShape{"ChangeTrustAsset"}, shI64,
	}})
	registerShape("AllowTrustOp", structShape{fields: []shape{
		refShape{"AccountID"}, unionShape{arms: map[uint32]shape{ // AssetCode union
			1: fixedOpaqueShape{n: 4},  // ASSET_TYPE_CREDIT_ALPHANUM4
			2: fixedOpaqueShape{n: 12}, // ASSET_TYPE_CREDIT_ALPHANUM12
		}}, shU32,
	}})
	registerShape("ManageDataOp", structShape{fields: []shape{
		shString64, optionalShape{shDataValue},
	}})
	registerShape("BumpSequenceOp", structShape{fields: []shape{shI64}})
	registerShape("ManageBuyOfferOp", structShape{fields: []shape{
		refShape{"Asset"}, refShape{"Asset"}, shI64, refShape{"Price"}, shI64,
	}})
	registerShape("PathPaymentStrictSendOp", structShape{fields: []shape{
		refShape{"Asset"}, refShape{"MuxedAccount"}, shI64, refShape{"Asset"}, shI64,
		varArrayShape{max: 5, inner: refShape{"Asset"}},
	}})
	registerShape("CreateClaimableBalanceOp", structShape{fields: []shape{
		refShape{"Asset"}, shI64,
		varArrayShape{max: 10, inner: refShape{"Claimant"}},
	}})
	registerShape("ClaimClaimableBalanceOp", structShape{fields: []shape{shOpaque32}})
	registerShape("BeginSponsoringFutureReservesOp", structShape{fields: []shape{
		refShape{"AccountID"},
	}})
	registerShape("ClawbackOp", structShape{fields: []shape{
		refShape{"Asset"}, refShape{"MuxedAccount"}, shI64,
	}})
	registerShape("ClawbackClaimableBalanceOp", structShape{fields: []shape{shOpaque32}})
	registerShape("SetTrustLineFlagsOp", structShape{fields: []shape{
		refShape{"AccountID"}, refShape{"Asset"}, shU32, shU32,
	}})
	registerShape("LiquidityPoolDepositOp", structShape{fields: []shape{
		shOpaque32, shI64, shI64, refShape{"Price"}, refShape{"Price"},
	}})
	registerShape("LiquidityPoolWithdrawOp", structShape{fields: []shape{
		shOpaque32, shI64, shI64,
	}})

	// LedgerKey union (classic accounts only; Soroban arms are rejected).
	registerShape("LedgerKey", unionShape{arms: map[uint32]shape{
		0: refShape{"AccountID"},
		1: structShape{fields: []shape{refShape{"AccountID"}, refShape{"Asset"}}},
		2: structShape{fields: []shape{refShape{"AccountID"}, shI64}},      // OFFER: seller, offerID
		3: structShape{fields: []shape{refShape{"AccountID"}, shString64}}, // DATA
		4: shOpaque32,
		5: shOpaque32,
	}})

	registerShape("RevokeSponsorshipOp", unionShape{arms: map[uint32]shape{
		0: refShape{"LedgerKey"},
		1: unionShape{arms: map[uint32]shape{
			0: refShape{"AccountID"},
			1: structShape{fields: []shape{refShape{"AccountID"}, refShape{"Asset"}}},
			2: structShape{fields: []shape{refShape{"AccountID"}, shI64}},
			3: structShape{fields: []shape{refShape{"AccountID"}, shString64}},
			4: shOpaque32,
			5: shOpaque32,
		}},
	}})

	// Operation body, keyed by OperationType discriminant 0..19.
	registerShape("OperationBody", unionShape{arms: map[uint32]shape{
		0:  refShape{"CreateAccountOp"},
		1:  refShape{"PaymentOp"},
		2:  refShape{"PathPaymentStrictReceiveOp"},
		3:  refShape{"ManageSellOfferOp"},
		4:  refShape{"CreatePassiveSellOfferOp"},
		5:  refShape{"SetOptionsOp"},
		6:  refShape{"ChangeTrustOp"},
		7:  refShape{"AllowTrustOp"},
		8:  refShape{"MuxedAccount"}, // ACCOUNT_MERGE: MuxedAccount payload
		9:  nil,                      // INFLATION
		10: refShape{"ManageDataOp"},
		11: refShape{"BumpSequenceOp"},
		12: refShape{"ManageBuyOfferOp"},
		13: refShape{"PathPaymentStrictSendOp"},
		14: refShape{"CreateClaimableBalanceOp"},
		15: refShape{"ClaimClaimableBalanceOp"},
		16: refShape{"BeginSponsoringFutureReservesOp"},
		17: nil, // END_SPONSORING_FUTURE_RESERVES
		18: refShape{"RevokeSponsorshipOp"},
		19: refShape{"ClawbackOp"},
		20: refShape{"ClawbackClaimableBalanceOp"},
		21: refShape{"SetTrustLineFlagsOp"},
		22: refShape{"LiquidityPoolDepositOp"},
		23: refShape{"LiquidityPoolWithdrawOp"},
	}})
}

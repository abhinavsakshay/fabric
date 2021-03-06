/*
Copyright IBM Corp. 2017 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

		 http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mcs

import (
	"errors"
	"fmt"

	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/bccsp/factory"
	"github.com/hyperledger/fabric/common/policies"
	"github.com/hyperledger/fabric/gossip/api"
	"github.com/hyperledger/fabric/gossip/common"
	"github.com/hyperledger/fabric/msp"
	"github.com/hyperledger/fabric/msp/mgmt"
	protoscommon "github.com/hyperledger/fabric/protos/common"
	"github.com/op/go-logging"
)

var logger = logging.MustGetLogger("peer/gossip/mcs")

// mspMessageCryptoService implements the MessageCryptoService interface
// using the peer MSPs (local and channel-related)
//
// In order for the system to be secure it is vital to have the
// MSPs to be up-to-date. Channels' MSPs are updated via
// configuration transactions distributed by the ordering service.
//
// A similar mechanism needs to be in place to update the local MSP, as well.
// This implementation assumes that these mechanisms are all in place and working.
type mspMessageCryptoService struct {
	manager policies.Manager
}

// New creates a new instance of mspMessageCryptoService
// that implements MessageCryptoService.
// The method takes in input a policy manager that gives
// access to the policy manager of a given channel via the Manager method.
// See fabric/core/peer/peer.go#NewPolicyManagerMgmt and
// fabric/common/mocks/policies/policies.go#PolicyManagerMgmt
func New(manager policies.Manager) api.MessageCryptoService {
	return &mspMessageCryptoService{manager: manager}
}

// ValidateIdentity validates the identity of a remote peer.
// If the identity is invalid, revoked, expired it returns an error.
// Else, returns nil
func (s *mspMessageCryptoService) ValidateIdentity(peerIdentity api.PeerIdentityType) error {
	// As prescibed by the contract of method,
	// here we check only that peerIdentity is not
	// invalid, revoked or expired.

	_, _, err := s.getValidatedIdentity(peerIdentity)
	return err
}

// GetPKIidOfCert returns the PKI-ID of a peer's identity
// If any error occurs, the method return nil
// The PKid of a peer is computed as the SHA2-256 of peerIdentity which
// is supposed to be the serialized version of MSP identity.
// This method does not validate peerIdentity.
// This validation is supposed to be done appropriately during the execution flow.
func (s *mspMessageCryptoService) GetPKIidOfCert(peerIdentity api.PeerIdentityType) common.PKIidType {
	// Validate arguments
	if len(peerIdentity) == 0 {
		logger.Error("Invalid Peer Identity. It must be different from nil.")

		return nil
	}

	// Hash
	digest, err := factory.GetDefault().Hash(peerIdentity, &bccsp.SHA256Opts{})
	if err != nil {
		logger.Errorf("Failed computing digest of serialized identity [% x]: [%s]", peerIdentity, err)

		return nil
	}

	return digest
}

// VerifyBlock returns nil if the block is properly signed,
// else returns error
func (s *mspMessageCryptoService) VerifyBlock(chainID common.ChainID, signedBlock api.SignedBlock) error {
	// TODO: to be implemented
	// Steps:
	// 1. Check that the block is related to chainID
	// 2. Verify that the block is properly signed
	//    using the policy associated to chainID

	return nil
}

// Sign signs msg with this peer's signing key and outputs
// the signature if no error occurred.
func (s *mspMessageCryptoService) Sign(msg []byte) ([]byte, error) {
	return mgmt.GetLocalSigningIdentityOrPanic().Sign(msg)
}

// Verify checks that signature is a valid signature of message under a peer's verification key.
// If the verification succeeded, Verify returns nil meaning no error occurred.
// If peerIdentity is nil, then the verification fails.
func (s *mspMessageCryptoService) Verify(peerIdentity api.PeerIdentityType, signature, message []byte) error {
	identity, chainID, err := s.getValidatedIdentity(peerIdentity)
	if err != nil {
		logger.Errorf("Failed getting validated identity from peer identity [%s]", err)

		return err
	}

	if len(chainID) == 0 {
		// At this stage, this means that peerIdentity
		// belongs to this peer's LocalMSP.
		// The signature is validated directly
		return identity.Verify(message, signature)
	}

	// At this stage, the signature must be validated
	// against the reader policy of the channel
	// identified by chainID

	return s.VerifyByChannel(chainID, peerIdentity, signature, message)
}

// VerifyByChannel checks that signature is a valid signature of message
// under a peer's verification key, but also in the context of a specific channel.
// If the verification succeeded, Verify returns nil meaning no error occurred.
// If peerIdentity is nil, then the verification fails.
func (s *mspMessageCryptoService) VerifyByChannel(chainID common.ChainID, peerIdentity api.PeerIdentityType, signature, message []byte) error {
	// Validate arguments
	if len(peerIdentity) == 0 {
		return errors.New("Invalid Peer Identity. It must be different from nil.")
	}

	// Get the policy manager for channel chainID
	cpm, flag := s.manager.Manager([]string{string(chainID)})
	logger.Debugf("Got policy manager for channel [%s] with flag [%s]", string(chainID), flag)

	// Get channel reader policy
	policy, flag := cpm.GetPolicy(policies.ChannelApplicationReaders)
	logger.Debugf("Got reader policy for channel [%s] with flag [%s]", string(chainID), flag)

	return policy.Evaluate(
		[]*protoscommon.SignedData{{
			Data:      message,
			Identity:  []byte(peerIdentity),
			Signature: signature,
		}},
	)
}

func (s *mspMessageCryptoService) getValidatedIdentity(peerIdentity api.PeerIdentityType) (msp.Identity, common.ChainID, error) {
	// Validate arguments
	if len(peerIdentity) == 0 {
		return nil, nil, errors.New("Invalid Peer Identity. It must be different from nil.")
	}

	// Notice that peerIdentity is assumed to be the serialization of an identity.
	// So, first step is the identity deserialization and then verify it.

	// First check against the local MSP.
	// If the peerIdentity is in the same organization of this node then
	// the local MSP is required to take the final decision on the validity
	// of the signature.
	identity, err := mgmt.GetLocalMSP().DeserializeIdentity([]byte(peerIdentity))
	if err != nil {
		// peerIdentity is NOT in the same organization of this node
		logger.Debugf("LocalMSP failed deserializing peer identity [% x]: [%s]", []byte(peerIdentity), err)
	} else {
		// TODO: The following check will be replaced by a check on the organizational units
		// when we allow the gossip network to have organization unit (MSP subdivisions)
		// scoped messages.
		// The following check is consistent with the SecurityAdvisor#OrgByPeerIdentity
		// implementation.
		// TODO: Notice that the following check saves us from the fact
		// that DeserializeIdentity does not yet enforce MSP-IDs consistency.
		// This check can be removed once DeserializeIdentity will be fixed.
		if identity.GetMSPIdentifier() == mgmt.GetLocalSigningIdentityOrPanic().GetMSPIdentifier() {
			// Check identity validity

			// Notice that at this stage we don't have to check the identity
			// against any channel's policies.
			// This will be done by the caller function, if needed.
			return identity, nil, identity.Validate()
		}
	}

	// Check against managers
	for chainID, mspManager := range mgmt.GetManagers() {
		// Deserialize identity
		identity, err := mspManager.DeserializeIdentity([]byte(peerIdentity))
		if err != nil {
			logger.Debugf("Failed deserialization identity [% x] on [%s]: [%s]", peerIdentity, chainID, err)
			continue
		}

		// Check identity validity
		// Notice that at this stage we don't have to check the identity
		// against any channel's policies.
		// This will be done by the caller function, if needed.

		if err := identity.Validate(); err != nil {
			logger.Debugf("Failed validating identity [% x] on [%s]: [%s]", peerIdentity, chainID, err)
			continue
		}

		logger.Debugf("Validation succesed  [% x] on [%s]", peerIdentity, chainID)

		return identity, common.ChainID(chainID), nil
	}

	return nil, nil, fmt.Errorf("Peer Identity [% x] cannot be validated. No MSP found able to do that.", peerIdentity)
}

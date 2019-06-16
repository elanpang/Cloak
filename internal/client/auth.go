package client

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"

	"github.com/cbeuw/Cloak/internal/ecdh"
	"github.com/cbeuw/Cloak/internal/util"
)

type keyPair struct {
	crypto.PrivateKey
	crypto.PublicKey
}

func MakeRandomField(sta *State) []byte {
	t := make([]byte, 8)
	binary.BigEndian.PutUint64(t, uint64(sta.Now().Unix()/(12*60*60)))
	rdm := make([]byte, 16)
	io.ReadFull(rand.Reader, rdm)
	preHash := make([]byte, 56)
	copy(preHash[0:32], sta.UID)
	copy(preHash[32:40], t)
	copy(preHash[40:56], rdm)
	h := sha256.New()
	h.Write(preHash)
	ret := make([]byte, 32)
	copy(ret[0:16], rdm)
	copy(ret[16:32], h.Sum(nil)[0:16])
	return ret
}

func MakeSessionTicket(sta *State) []byte {
	// sessionTicket: [marshalled ephemeral pub key 32 bytes][encrypted UID+sessionID 20 bytes, proxy method 16 bytes, encryption method 1 byte, sessionKey 32 bytes][16 bytes authentication tag][padding 75 bytes]
	// The first 12 bytes of the marshalled ephemeral public key is used as the nonce
	// for encrypting the UID
	tthInterval := sta.Now().Unix() / int64(sta.TicketTimeHint)
	sta.keyPairsM.Lock()
	ephKP := sta.keyPairs[tthInterval]
	if ephKP == nil {
		ephPv, ephPub, _ := ecdh.GenerateKey(rand.Reader)
		ephKP = &keyPair{
			ephPv,
			ephPub,
		}
		sta.keyPairs[tthInterval] = ephKP
	}
	sta.keyPairsM.Unlock()
	ticket := make([]byte, 192)
	copy(ticket[0:32], ecdh.Marshal(ephKP.PublicKey))
	key := ecdh.GenerateSharedSecret(ephKP.PrivateKey, sta.staticPub)

	plain := make([]byte, 69)
	copy(plain, sta.UID)
	binary.BigEndian.PutUint32(plain[16:20], sta.sessionID)
	copy(plain[20:36], []byte(sta.ProxyMethod))
	plain[36] = sta.EncryptionMethod
	copy(plain[37:69], sta.SessionKey)

	cipher, _ := util.AESGCMEncrypt(ticket[0:12], key, plain)
	copy(ticket[32:117], cipher)
	// The purpose of adding sessionID is that, the generated padding of sessionTicket needs to be unpredictable.
	// As shown in auth.go, the padding is generated by a psudo random generator. The seed
	// needs to be the same for each TicketTimeHint interval. However the value of epoch/TicketTimeHint
	// is public knowledge, so is the psudo random algorithm used by math/rand. Therefore not only
	// can the firewall tell that the padding is generated in this specific way, this padding is identical
	// for all ckclients in the same TicketTimeHint interval. This will expose us.
	//
	// With the sessionID value generated at startup of ckclient and used as a part of the seed, the
	// sessionTicket is still identical for each TicketTimeHint interval, but others won't be able to know
	// how it was generated. It will also be different for each client.
	copy(ticket[117:192], util.PsudoRandBytes(75, tthInterval+int64(sta.sessionID)))
	return ticket
}

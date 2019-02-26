package iwallet

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/iost-official/go-iost/account"
	"github.com/iost-official/go-iost/common"
	"github.com/iost-official/go-iost/crypto"
	"github.com/iost-official/go-iost/rpc/pb"
	"github.com/iost-official/go-iost/sdk"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
)

func checkArgsNumber(cmd *cobra.Command, args []string, argNames ...string) error {
	if len(args) < len(argNames) {
		cmd.Help()
		fmt.Println()
		return fmt.Errorf("missing positional argument: %v", argNames[len(args):])
	}
	return nil
}

func checkAccount(cmd *cobra.Command) error {
	if accountName == "" {
		cmd.Help()
		fmt.Println()
		return fmt.Errorf("please provide the account name with flag --account")
	}
	return nil
}

func checkFloat(cmd *cobra.Command, arg string, argName string) error {
	_, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		cmd.Help()
		fmt.Println()
		return fmt.Errorf(`invalid value "%v" for argument "%v": %v`, arg, argName, err)
	}
	return nil
}

func checkSigners(signers []string) error {
	for _, s := range signers {
		if !(len(strings.Split(s, "@")) == 2) {
			return fmt.Errorf("signer %v should contain '@'", s)
		}
	}
	return nil
}

func getAccountDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return home + "/.iwallet", nil
}

// GetSignAlgoByName ...
func GetSignAlgoByName(name string) crypto.Algorithm {
	switch name {
	case "secp256k1":
		return crypto.Secp256k1
	case "ed25519":
		return crypto.Ed25519
	default:
		return crypto.Ed25519
	}
}

func loadAccountByName(name string, ensureDecrypt bool) (*AccountInfo, error) {
	accountDir, err := getAccountDir()
	if err != nil {
		return nil, err
	}
	fileName := accountDir + "/" + name + ".json"
	if _, err := os.Stat(fileName); err == nil {
		return loadAccountFromKeyStore(fileName, ensureDecrypt)
	}
	for _, algo := range ValidSignAlgos {
		fileName := accountDir + "/" + name + "_" + algo
		if _, err := os.Stat(fileName); err == nil {
			return loadAccountFromKeyPair(fileName)
		}
	}
	return nil, fmt.Errorf("account not exist")
}

// LoadKeyPair ...
func LoadKeyPair(name string) (*account.KeyPair, error) {
	if name == "" {
		return nil, fmt.Errorf("you must provide account name")
	}
	dir, err := getAccountDir()
	if err != nil {
		return nil, err
	}
	privKeyFile := fmt.Sprintf("%s/%s_%s", dir, name, signAlgo)
	return sdk.LoadKeyPair(privKeyFile, signAlgo)
}

// InitAccount load account from file
func InitAccount() error {
	return LoadAndSetAccountForSDK(iwalletSDK)
}

// LoadAndSetAccountForSDK ...
func LoadAndSetAccountForSDK(s *sdk.IOSTDevSDK) error {
	a, err := loadAccountByName(accountName, true)
	if err != nil {
		return err
	}
	kp, ok := a.Keypairs[signPerm]
	if !ok {
		return fmt.Errorf("invalid permission %v", signPerm)
	}
	keyPair, err := kp.toKeyPair()
	if err != nil {
		return err
	}
	s.SetAccount(accountName, keyPair)
	return nil
}

// SaveAccount save account to file
func SaveAccount(name string, kp *account.KeyPair) error {
	dir, err := getAccountDir()
	if err != nil {
		return err
	}
	err = os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}
	fileName := dir + "/" + name
	if kp.Algorithm == crypto.Ed25519 {
		fileName += "_ed25519"
	}
	if kp.Algorithm == crypto.Secp256k1 {
		fileName += "_secp256k1"
	}

	pubfile, err := os.Create(fileName + ".pub")
	if err != nil {
		return fmt.Errorf("create file %v err %v", fileName+".pub", err)
	}
	defer pubfile.Close()

	_, err = pubfile.WriteString(common.Base58Encode(kp.Pubkey))
	if err != nil {
		return err
	}

	secFile, err := os.OpenFile(fileName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0400)
	if err != nil {
		return fmt.Errorf("create file %v err %v", fileName, err)
	}
	defer secFile.Close()

	_, err = secFile.WriteString(common.Base58Encode(kp.Seckey))
	if err != nil {
		return err
	}

	fmt.Println("Your account private key is saved at:", fileName)
	return nil
}

func actionsFromFlags(args []string) ([]*rpcpb.Action, error) {
	argc := len(args)
	if argc%3 != 0 {
		return nil, fmt.Errorf(`number of args should be a multiplier of 3`)
	}
	var actions = make([]*rpcpb.Action, 0)
	for i := 0; i < len(args); i += 3 {
		act := sdk.NewAction(args[i], args[i+1], args[i+2]) // Add some checks here.
		actions = append(actions, act)
	}
	return actions, nil
}

func handleMultiSig(t *rpcpb.TransactionRequest, withSigns []string, signKeys []string) error {
	sigs := make([]*rpcpb.Signature, 0)
	if len(withSigns) != 0 && len(signKeys) != 0 {
		return fmt.Errorf("at least one of --sign_keys and --with_signs should be empty")
	}
	if len(signKeys) > 0 {
		for _, f := range signKeys {
			kp, err := sdk.LoadKeyPair(f, signAlgo)
			if err != nil {
				return fmt.Errorf("sign tx with priv key %v err %v", f, err)
			}
			sigs = append(sigs, sdk.GetSignatureOfTx(t, kp))
		}
	} else if len(withSigns) > 0 {
		for _, f := range withSigns {
			sig := &rpcpb.Signature{}
			err := sdk.LoadProtoStructFromJSONFile(f, sig)
			if err != nil {
				return fmt.Errorf("invalid signature file %v", f)
			}
			if !sdk.VerifySigForTx(t, sig) {
				return fmt.Errorf("sign verify error %v", f)
			}
			sigs = append(sigs, sig)
		}
	}
	t.Signatures = sigs
	return nil
}

// ParseAmountLimit ...
func ParseAmountLimit(limitStr string) ([]*rpcpb.AmountLimit, error) {
	result := make([]*rpcpb.AmountLimit, 0)
	if limitStr == "" {
		return result, nil
	}
	splits := strings.Split(limitStr, "|")
	for _, gram := range splits {
		limit := strings.Split(gram, ":")
		if len(limit) != 2 {
			return nil, fmt.Errorf("invalid amount limit %v", gram)
		}
		token := limit[0]
		if limit[1] != "unlimited" {
			amountLimit, err := strconv.ParseFloat(limit[1], 64)
			if err != nil {
				return nil, fmt.Errorf("invalid amount limit %v %v", amountLimit, err)
			}
		}
		tokenLimit := &rpcpb.AmountLimit{}
		tokenLimit.Token = token
		tokenLimit.Value = limit[1]
		result = append(result, tokenLimit)
	}
	return result, nil
}

// ValidSignAlgos ...
var ValidSignAlgos = []string{"ed25519", "secp256k1"}

func getAccountNameFromKeyPath(file string, suf string) (string, error) {
	f := file
	startIndex := strings.LastIndex(f, "/")
	//if startIndex == -1 {
	//	return "", fmt.Errorf("file name error, no '/' in %v", f)
	//}

	lastIndex := strings.LastIndex(f, suf)
	if lastIndex == -1 {
		return "", fmt.Errorf("file name error, no %v in %v", suf, f)
	}

	return f[startIndex+1 : lastIndex], nil
}

func getFilesAndDirs(dirPth string, suf string) (files []string, err error) { // nolint
	dir, err := ioutil.ReadDir(dirPth)
	if err != nil {
		return nil, err
	}

	PthSep := string(os.PathSeparator)
	for _, fi := range dir {
		if !fi.IsDir() {
			ok := strings.HasSuffix(fi.Name(), suf)
			if ok {
				files = append(files, dirPth+PthSep+fi.Name())
			}
		}
	}

	return files, nil
}

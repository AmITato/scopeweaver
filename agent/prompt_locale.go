package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/Autumn-27/artex/locale"
)

// Hashes identify exact upstream stock templates without storing or rewriting
// arbitrary historical/user templates. Any edited byte prevents this match.
var legacyPromptDefaults = map[string]string{
	"0688796e309376cf6eb8b3146d4490ff6c6173d9fed95cdef04bcd9f034c7599": goalsDefaultTmpl,  // prior stock ja
	"07114305d483936b14655d61d914ace1ba17724e77d228d7a7f59ce2c383b8d2": workerDefaultTmpl, // prior stock ja
	"0accde46829d54a89d25f8eade337266634a9f2fdfc005f30303e032dd50bc6d": goalsScopeTail,    // prior stock en
	"0afba34ad25d8dfe87bac7596dcaeb59040049ce5c412bd62c04f5881ad5dcce": plannerWrapUpDefault,
	"0dd2afa31bd0077ad050ff9462b8a645eda98c0839a08b8248edfc3c6a672a4b": ReporterDefaultPrompt, // prior stock zh-Hant
	"0fbef372d1c6c29ef9927525d19048bdac1061fdef06e58edda15d5035e4d289": autoDefaultTmpl,       // prior stock zh-Hant
	"10aec9c93848d8de6fae09c085d13e290f059ae547c0b1fa96a177a5332d60b7": goalsDefaultTmpl,      // prior stock ko
	"12bb69ee0886c4fb9beb936d882021a91104860f1da69a5e3eda3d9e345cd399": mainAgentDefaultTmpl,
	"138e1e322c3d846a9fd8f5c4312ea209de03a86b8cfb5b28eac9c3ab88287db4": goalsDefaultTmpl,   // prior stock zh-Hant
	"14be02ee9efb779492447eeefec1dd2f915896435b44118f867739eacba6517d": pentestDefaultTmpl, // prior stock es
	"14e163f4a44da9fee20a214048a607adc3659b2025275569eb512532ca8c15d5": plannerDefaultTmpl,
	"15a5f07317e1aafec9a801d818ab122f3cc15d484b1330db54fd9582b4f2471d": plannerDefaultTmpl,   // prior Japanese stock planner
	"16dc0f02e2a9a8f5073425aff9b632df2c1fb06e4a513703af78c2d935c0b372": mainAgentDefaultTmpl, // prior stock ja
	"1b40bab849dadbc9350acc39cf19b4ad7845c70b1353025809e69048cf8b4d67": mainAgentDefaultTmpl, // prior stock en
	"1c8169ae5210b3637567bd18db3263706839f0a5f818d050d7d43866766a3828": workerDefaultTmpl,    // prior Japanese stock worker
	"2157083f36a87d5255308054d35492172542b9b5c99780a7e5db1e122b6eacac": workerDefaultTmpl,    // prior zh-Hans stock worker (bare-items add_intent era)
	"2613bf7d5d9fdc2f549667355eb747d92f53de2be3756a57361732aed8fd7ebe": settleWrapUpPrompt,
	"26b261499c89a196aa59a8b61da02198b908e8d1b5e2dc12d9a011f207aeb401": RetesterDefaultPrompt, // prior stock es
	"28457050d3aa564775822c367d41645b51587d66c8db4ac41aeb40403390ac94": ReporterDefaultPrompt, // prior stock en
	"29a3935436ab5f8ea7cd8a68581442e28191ab1d977a1e3b6f103cb1a24ade51": workerDefaultTmpl,     // prior stock en
	"29ef0d6c000a5a1b1af07c03b10528bb59be7540df427c68f09fe3b1e36da607": autoDefaultTmpl,       // prior stock ja
	"2f714365306c82e2ba7a30e95a40dd477fa5709b2ec3f3d37f36cbbc17cd0766": workerDefaultTmpl,     // prior stock zh-Hant
	"2ffd951bfdc5a151748161825d877a3212b656e05c2a32253540c980062ac108": plannerDefaultTmpl,    // prior stock zh-Hant
	"31fc3a067d0950239aca454e2f66001a560999196198762109cd3f4f8c7f2364": pentestDefaultTmpl,    // prior stock zh-Hant
	"3d8e683cc7aef99bae528eec3ad786cb42d8d01cf2084503fc07e383b8513733": pentestDefaultTmpl,    // prior stock ko
	"3e6eb9f3701f6e47e7194677d5b81914038e70f869dfbf8b462919c589a6a774": workerDefaultTmpl,     // prior stock zh-Hans
	"43f6f9af07ba51cec83a25b08887b6bcd45c691197a9b1c9df2d32e10b52748d": plannerDefaultTmpl,    // prior zh-Hans stock planner (facts/intents parent_ids era)
	"46677c4019250630ceaaa7190159841db68a4329a7ca36006dde29a93c6ce47a": RetesterDefaultPrompt, // prior stock ko
	"48945375772a662d10cf3d1d31167d5d8d3e906166d7dd3bd264c1a49d932335": RetesterDefaultPrompt, // prior stock ja
	"49f10e898b46c0699dba3df378347fb19ceb1e40f3fc19355c7e4dd78b3798c2": goalsScopeTail,        // prior stock ko
	"4ea4eb90f64b6622fb826ea9ede14ce025c2a42fa4109ddaf67a415bb631d990": mainAgentDefaultTmpl,  // prior stock zh-Hans
	"51198cba5c6bce3fdf11c7f17f39fe2d9ea719e01a37cc805bf6a434a0e26fac": autoDefaultTmpl,       // prior stock es
	"518d5560b4bf36fe7e8b1f777b0a2581a7f46088708165005d43d9170030149c": workerDefaultTmpl,     // prior stock es
	"52333c82468d757ce3818c8c88379f9d1e7488d9bc388d116abc3423fe9529fa": mainAgentWrapUpDefault,
	"53b00ed7f346ed751c12a0907a2ecdcf8753a34bb87d3f69fb54a531db17d9a4": workerDefaultTmpl,     // prior Korean stock worker
	"53e662e1360f8b2fd3eb96cbf437a47175f9b195fd8f937e5e8311f1f2b6eb2d": goalsScopeTail,        // prior stock zh-Hant
	"58b2f88bd1fe969b3ddf5d4f9d3dae641d9226ae4e5cfad32c3034ed6e77db0a": autoDefaultTmpl,       // prior stock en
	"6113f3deecb7d661bca74389956d0ab69e61571fc8672ee32a43f57b4a63d32d": plannerDefaultTmpl,    // prior stock es
	"6617232abdbfb288896d82907a4d7095d394752ff42f1c5a3f393035b4e7ab56": ReporterDefaultPrompt, // prior stock es
	"6b81f9d039f5b977586607e4ea719e8141d8e559104d3635d77b08e4ea9f036f": ReporterDefaultPrompt, // prior stock ja
	"72a7a0159443b41dde29dfba8a3de6a2c8a15b273935495a7b88561fa708aa68": goalsDefaultTmpl,
	"77cb759094113af7cc08441dfe3005b224df46050fc9e6f073b51d8c92fc035a": plannerDefaultTmpl, // prior Traditional Chinese stock planner
	"80fbb1c610dd57e266c20b9145793339eee540b809f6db8f5336261b45879f08": workerDefaultTmpl,
	"85b13bf53c66969efc630a28e9174158cb6b7dadfc4a1ffa8e30b2084437aebb": plannerDefaultTmpl,    // prior stock zh-Hans
	"86925f59722414a931d89e7f5bae2565ef5a5a67ede875513cbdbfd6999a9124": RetesterDefaultPrompt, // prior stock zh-Hans
	"897cb41929a12ad4e8c7e80d0c104c8b3483a71fbad56a45d92b4822d40bf16c": goalsDefaultTmpl,      // prior stock en
	"8ba98208f6c5f2ccb103c88ef13239c4264733400c0e3dfec96d3a3572d78d39": ReporterDefaultPrompt,
	"8be527464f509db0288cd982b600fe80a49c1f5988391b2f84c707f8a31b5a76": mainAgentDefaultTmpl,  // prior stock es
	"8ef39d93020a54215ced394054643816f38b798e1cd4436ae4c1f648cf8b383b": pentestDefaultTmpl,    // prior stock ja
	"8f64e44e040a326603372fd67522bc053d02ea182842ebb5d5ef1c2d8158e0ef": mainAgentDefaultTmpl,  // prior stock zh-Hant
	"90f03f76f07110ffa8eca53a3b1be98e5970608604119e4c6191b64edeae7d4f": goalsDefaultTmpl,      // prior stock zh-Hans
	"92f02f26aba2e57146cf014a6d9d0370f7bf6eb6f09e597e1715b48c89bae561": autoDefaultTmpl,       // prior stock zh-Hans
	"96c798142e2d6b20a2896e1d40c701b940e17c991acf2519ea9796b960fac60c": pentestDefaultTmpl,    // prior stock zh-Hans
	"9955e6b6587e48997d8a4f9a0dba6873020a4cb155a7ee551eafa93ae6430ad7": RetesterDefaultPrompt, // prior stock en
	"9d0b47458ae54a85cad722d7078eacbe40a11e40e6e7aef774bd8610a61a08b1": genericWrapUpDefault,
	"a12986f7c23d22782c01fa0292d6f78d89591bbc7a6c6724eb4f4acbbe03c134": ReporterDefaultPrompt, // prior stock ko
	"a179c990bff1ab4e0a6f326945a1c44036ef4c8a54c7c5b635965f6cefdc9f0a": DefaultAssistantPrompt,
	"a54bd5713b1c121d0f2ff3a72e0304820b1368c4b4b8159d5d651895663ac652": pentestDefaultTmpl,
	"b0f24100fa4661d5df99160b64c954d4bebf374c70f98ac8f81bb4396e63bab6": workerDefaultTmpl, // prior English stock worker
	"b26a559c7d415c9a7e1ead2d1ca84ef49bd5335afd8f6ef53090aa225b53431d": autoDefaultTmpl,   // prior stock ko
	"b2a71b82243607337b19f0642317fed88c144d536412d80eaaf90807f2905a1d": autoDefaultTmpl,
	"bbefbdf2059d09703a0c6c665d4b4734f99c4df270974bb10c795d987b672343": workerTaskTimeoutDefault,
	"c2c3d0638e9f90eb9948074c2262dc9af907b038d37b68e6940eacd177c16a70": plannerDefaultTmpl,    // prior stock en
	"c6b651d9f8bafe6f7a900b5bdd71f0a0a8be50f26b15a11e29bb3cecd4b6aeef": pentestDefaultTmpl,    // prior stock en
	"cb16efe46cdc109ea17d379a2a339f1abc426d10cfdf607a030fc17a8f1ebd35": goalsScopeTail,        // prior stock es
	"cce6c9cb3695ed64bfb20b1da3eb9fe51a922867269fdc1a7bf8cacb92447b71": RetesterDefaultPrompt, // prior stock zh-Hant
	"d002b51c2a5c42f8197714f5b86b11539f85c3451a19e87472e8483ed0adfd76": plannerTaskTimeoutDefault,
	"d08326aa9fbd9a1367e716cf7ef46533e9096e9a56493f77d364efbf696f7fe0": RetesterDefaultPrompt,
	"d24489da70bb388bb00f3ae647a9d937b45f60b3b5ca9c9a09caff75cdd69e5a": plannerDefaultTmpl,    // prior English stock planner
	"d5dbf5389bfc3468f91a6c2edf72716658008f04b0e90cdf1b22294c2cdb8c74": mainAgentDefaultTmpl,  // prior stock ko
	"daec2dffeb21cd6789a69c1502d91508203b8b6f9e1fe35a8f359f846972e78b": goalsScopeTail,        // prior stock zh-Hans
	"dc38b31649d4634273e3130d680abf04604a634629495089b222177b1d1cadd5": workerDefaultTmpl,     // prior stock ko
	"df4278942ff84cdd219e6301be09af23ebc209a35d6dd2d45483ff3b5fd8b43b": plannerDefaultTmpl,    // prior Korean stock planner
	"e6b5c4d123a6863a1892ebb328faa3740689ea774ab3476dc2b254739afc5467": workerDefaultTmpl,     // prior Spanish stock worker
	"e82013ede59cb614844e430dd9a3ba69254eb317053f98b5d0a338b70e95862f": plannerDefaultTmpl,    // prior Spanish stock planner
	"efdf9b260d9aaeede63d69cc6618d4e11193fc018e8d8fc27049f422b8625f8b": plannerDefaultTmpl,    // prior stock ja
	"f39ef1637f4c9dbaf8b2181b522a59d3dedb1da99d17ceec9beb9ab0126b2e07": ReporterDefaultPrompt, // prior stock zh-Hans
	"fb11a4afee4d6ee56552a5235f09fcb771889720fa5ac94e91a5763cab2e1cf0": plannerDefaultTmpl,    // prior stock ko
	"fc96bfa719979368e5d84196c783b531ef33cb11dc295db2867a0264e6e1ec01": workerDefaultTmpl,     // prior Traditional Chinese stock worker
	"fd0d8358ca9a6a4f5545e018fb01a8a098f907821804a8975fe6c202e2d2a8cf": goalsScopeTail,        // prior stock ja
	"fd5d2a3ed51d28360fd6b29af485f544df049af7711d91ba6f8358b349889574": goalsDefaultTmpl,      // prior stock es
}

// CanonicalBuiltinPrompt returns canonical English only for an exact stock
// shipped-language/legacy template. All other text is returned byte-for-byte.
func CanonicalBuiltinPrompt(text string) string {
	for _, builtin := range []string{goalsDefaultTmpl, goalsScopeTail, opsecBlockText, plannerDefaultTmpl, workerDefaultTmpl, mainAgentDefaultTmpl, autoDefaultTmpl, pentestDefaultTmpl, DefaultAssistantPrompt, ReporterDefaultPrompt, RetesterDefaultPrompt, settleWrapUpPrompt, plannerWrapUpDefault, mainAgentWrapUpDefault, genericWrapUpDefault, workerTaskTimeoutDefault, plannerTaskTimeoutDefault} {
		for _, lang := range locale.Languages() {
			if text == locale.Text(lang, builtin) {
				return builtin
			}
		}
	}
	digest := sha256.Sum256([]byte(text))
	if builtin, ok := legacyPromptDefaults[hex.EncodeToString(digest[:])]; ok {
		return builtin
	}
	return text
}

func BuiltinPromptText(text string, lang locale.Lang) string {
	canonical := CanonicalBuiltinPrompt(text)
	for _, builtin := range []string{goalsDefaultTmpl, goalsScopeTail, opsecBlockText, plannerDefaultTmpl, workerDefaultTmpl, mainAgentDefaultTmpl, autoDefaultTmpl, pentestDefaultTmpl, DefaultAssistantPrompt, ReporterDefaultPrompt, RetesterDefaultPrompt, settleWrapUpPrompt, plannerWrapUpDefault, mainAgentWrapUpDefault, genericWrapUpDefault, workerTaskTimeoutDefault, plannerTaskTimeoutDefault} {
		if canonical == builtin {
			return locale.Text(lang, builtin)
		}
	}
	return text
}

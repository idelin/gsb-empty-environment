# SDP改写检测

信令链路上的攻击者把DTLS指纹或候选地址改成自己的，以窃听媒体。为TypeScript信令网关实现可导入的SDP改写检测包：与createOffer原稿比，a=fingerprint、ice-ufrag、ice-pwd、mid及媒体段数不得变；只许同一m行内重排payload类型；新增host候选须在登记网段。用换指纹、增删m段和私网候选测试。

# RAR授权细节子集判定

攻击者在授权请求里夹带比用户同意范围更宽的 authorization_details，或刷新时扩权。为Go授权服务器写可导入的 RAR 子集判定包：请求项被满足当且仅当存在同 type 的已授权项，其 locations 逐字节包含请求 URL（不做前缀匹配），actions、datatypes 为超集，identifier 相同或请求未给；未知 type 使整个请求被拒，刷新所得详情须是原授权的子集，重复项只计一次。测越权 URL、前缀仿冒、未知类型、刷新扩权。

// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        v4.24.0
// source: statedb.proto

package grpc

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type MetaRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *MetaRequest) Reset() {
	*x = MetaRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *MetaRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MetaRequest) ProtoMessage() {}

func (x *MetaRequest) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MetaRequest.ProtoReflect.Descriptor instead.
func (*MetaRequest) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{0}
}

type MetaResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Table []*Table `protobuf:"bytes,1,rep,name=table,proto3" json:"table,omitempty"`
}

func (x *MetaResponse) Reset() {
	*x = MetaResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *MetaResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MetaResponse) ProtoMessage() {}

func (x *MetaResponse) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MetaResponse.ProtoReflect.Descriptor instead.
func (*MetaResponse) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{1}
}

func (x *MetaResponse) GetTable() []*Table {
	if x != nil {
		return x.Table
	}
	return nil
}

type Table struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name  string   `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Index []string `protobuf:"bytes,2,rep,name=index,proto3" json:"index,omitempty"`
}

func (x *Table) Reset() {
	*x = Table{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Table) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Table) ProtoMessage() {}

func (x *Table) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Table.ProtoReflect.Descriptor instead.
func (*Table) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{2}
}

func (x *Table) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Table) GetIndex() []string {
	if x != nil {
		return x.Index
	}
	return nil
}

type QueryRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Table string `protobuf:"bytes,1,opt,name=table,proto3" json:"table,omitempty"`
	Index string `protobuf:"bytes,2,opt,name=index,proto3" json:"index,omitempty"`
	// key is a full key or a key prefix. Generated by
	// Index[...].FromKey.
	Key []byte `protobuf:"bytes,3,opt,name=key,proto3" json:"key,omitempty"`
	// limit is the number of matching objects to return.
	// If zero all matching objects are streamed.
	Limit uint32 `protobuf:"varint,4,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (x *QueryRequest) Reset() {
	*x = QueryRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *QueryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*QueryRequest) ProtoMessage() {}

func (x *QueryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use QueryRequest.ProtoReflect.Descriptor instead.
func (*QueryRequest) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{3}
}

func (x *QueryRequest) GetTable() string {
	if x != nil {
		return x.Table
	}
	return ""
}

func (x *QueryRequest) GetIndex() string {
	if x != nil {
		return x.Index
	}
	return ""
}

func (x *QueryRequest) GetKey() []byte {
	if x != nil {
		return x.Key
	}
	return nil
}

func (x *QueryRequest) GetLimit() uint32 {
	if x != nil {
		return x.Limit
	}
	return 0
}

type WatchRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Table string `protobuf:"bytes,1,opt,name=table,proto3" json:"table,omitempty"`
}

func (x *WatchRequest) Reset() {
	*x = WatchRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *WatchRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*WatchRequest) ProtoMessage() {}

func (x *WatchRequest) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use WatchRequest.ProtoReflect.Descriptor instead.
func (*WatchRequest) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{4}
}

func (x *WatchRequest) GetTable() string {
	if x != nil {
		return x.Table
	}
	return ""
}

type WatchResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Object  *Object `protobuf:"bytes,1,opt,name=object,proto3" json:"object,omitempty"`
	Deleted bool    `protobuf:"varint,2,opt,name=deleted,proto3" json:"deleted,omitempty"`
}

func (x *WatchResponse) Reset() {
	*x = WatchResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *WatchResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*WatchResponse) ProtoMessage() {}

func (x *WatchResponse) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use WatchResponse.ProtoReflect.Descriptor instead.
func (*WatchResponse) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{5}
}

func (x *WatchResponse) GetObject() *Object {
	if x != nil {
		return x.Object
	}
	return nil
}

func (x *WatchResponse) GetDeleted() bool {
	if x != nil {
		return x.Deleted
	}
	return false
}

type Object struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Revision uint64 `protobuf:"varint,1,opt,name=revision,proto3" json:"revision,omitempty"`
	Value    []byte `protobuf:"bytes,2,opt,name=value,proto3" json:"value,omitempty"` // gob encoded
}

func (x *Object) Reset() {
	*x = Object{}
	if protoimpl.UnsafeEnabled {
		mi := &file_statedb_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Object) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Object) ProtoMessage() {}

func (x *Object) ProtoReflect() protoreflect.Message {
	mi := &file_statedb_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Object.ProtoReflect.Descriptor instead.
func (*Object) Descriptor() ([]byte, []int) {
	return file_statedb_proto_rawDescGZIP(), []int{6}
}

func (x *Object) GetRevision() uint64 {
	if x != nil {
		return x.Revision
	}
	return 0
}

func (x *Object) GetValue() []byte {
	if x != nil {
		return x.Value
	}
	return nil
}

var File_statedb_proto protoreflect.FileDescriptor

var file_statedb_proto_rawDesc = []byte{
	0x0a, 0x0d, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x07, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x22, 0x0d, 0x0a, 0x0b, 0x4d, 0x65, 0x74, 0x61,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x22, 0x34, 0x0a, 0x0c, 0x4d, 0x65, 0x74, 0x61, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x24, 0x0a, 0x05, 0x74, 0x61, 0x62, 0x6c, 0x65,
	0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0e, 0x2e, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62,
	0x2e, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x52, 0x05, 0x74, 0x61, 0x62, 0x6c, 0x65, 0x22, 0x31, 0x0a,
	0x05, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x69, 0x6e,
	0x64, 0x65, 0x78, 0x18, 0x02, 0x20, 0x03, 0x28, 0x09, 0x52, 0x05, 0x69, 0x6e, 0x64, 0x65, 0x78,
	0x22, 0x62, 0x0a, 0x0c, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x14, 0x0a, 0x05, 0x74, 0x61, 0x62, 0x6c, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x05, 0x74, 0x61, 0x62, 0x6c, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x12, 0x10, 0x0a, 0x03,
	0x6b, 0x65, 0x79, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14,
	0x0a, 0x05, 0x6c, 0x69, 0x6d, 0x69, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x05, 0x6c,
	0x69, 0x6d, 0x69, 0x74, 0x22, 0x24, 0x0a, 0x0c, 0x57, 0x61, 0x74, 0x63, 0x68, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x14, 0x0a, 0x05, 0x74, 0x61, 0x62, 0x6c, 0x65, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x05, 0x74, 0x61, 0x62, 0x6c, 0x65, 0x22, 0x52, 0x0a, 0x0d, 0x57, 0x61,
	0x74, 0x63, 0x68, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x27, 0x0a, 0x06, 0x6f,
	0x62, 0x6a, 0x65, 0x63, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0f, 0x2e, 0x73, 0x74,
	0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x52, 0x06, 0x6f, 0x62,
	0x6a, 0x65, 0x63, 0x74, 0x12, 0x18, 0x0a, 0x07, 0x64, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x64, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x08, 0x52, 0x07, 0x64, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x64, 0x22, 0x3a,
	0x0a, 0x06, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x12, 0x1a, 0x0a, 0x08, 0x72, 0x65, 0x76, 0x69,
	0x73, 0x69, 0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x04, 0x52, 0x08, 0x72, 0x65, 0x76, 0x69,
	0x73, 0x69, 0x6f, 0x6e, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x0c, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x32, 0xe1, 0x01, 0x0a, 0x07, 0x53,
	0x74, 0x61, 0x74, 0x65, 0x44, 0x42, 0x12, 0x33, 0x0a, 0x04, 0x4d, 0x65, 0x74, 0x61, 0x12, 0x14,
	0x2e, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x4d, 0x65, 0x74, 0x61, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x15, 0x2e, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x4d,
	0x65, 0x74, 0x61, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x2f, 0x0a, 0x03, 0x47,
	0x65, 0x74, 0x12, 0x15, 0x2e, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x51, 0x75, 0x65,
	0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x0f, 0x2e, 0x73, 0x74, 0x61, 0x74,
	0x65, 0x64, 0x62, 0x2e, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x30, 0x01, 0x12, 0x36, 0x0a, 0x0a,
	0x4c, 0x6f, 0x77, 0x65, 0x72, 0x42, 0x6f, 0x75, 0x6e, 0x64, 0x12, 0x15, 0x2e, 0x73, 0x74, 0x61,
	0x74, 0x65, 0x64, 0x62, 0x2e, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x0f, 0x2e, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x4f, 0x62, 0x6a, 0x65,
	0x63, 0x74, 0x30, 0x01, 0x12, 0x38, 0x0a, 0x05, 0x57, 0x61, 0x74, 0x63, 0x68, 0x12, 0x15, 0x2e,
	0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x57, 0x61, 0x74, 0x63, 0x68, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x73, 0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2e, 0x57,
	0x61, 0x74, 0x63, 0x68, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x30, 0x01, 0x42, 0x2b,
	0x5a, 0x29, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x69, 0x6c,
	0x69, 0x75, 0x6d, 0x2f, 0x63, 0x69, 0x6c, 0x69, 0x75, 0x6d, 0x2f, 0x70, 0x6b, 0x67, 0x2f, 0x73,
	0x74, 0x61, 0x74, 0x65, 0x64, 0x62, 0x2f, 0x67, 0x72, 0x70, 0x63, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_statedb_proto_rawDescOnce sync.Once
	file_statedb_proto_rawDescData = file_statedb_proto_rawDesc
)

func file_statedb_proto_rawDescGZIP() []byte {
	file_statedb_proto_rawDescOnce.Do(func() {
		file_statedb_proto_rawDescData = protoimpl.X.CompressGZIP(file_statedb_proto_rawDescData)
	})
	return file_statedb_proto_rawDescData
}

var file_statedb_proto_msgTypes = make([]protoimpl.MessageInfo, 7)
var file_statedb_proto_goTypes = []interface{}{
	(*MetaRequest)(nil),   // 0: statedb.MetaRequest
	(*MetaResponse)(nil),  // 1: statedb.MetaResponse
	(*Table)(nil),         // 2: statedb.Table
	(*QueryRequest)(nil),  // 3: statedb.QueryRequest
	(*WatchRequest)(nil),  // 4: statedb.WatchRequest
	(*WatchResponse)(nil), // 5: statedb.WatchResponse
	(*Object)(nil),        // 6: statedb.Object
}
var file_statedb_proto_depIdxs = []int32{
	2, // 0: statedb.MetaResponse.table:type_name -> statedb.Table
	6, // 1: statedb.WatchResponse.object:type_name -> statedb.Object
	0, // 2: statedb.StateDB.Meta:input_type -> statedb.MetaRequest
	3, // 3: statedb.StateDB.Get:input_type -> statedb.QueryRequest
	3, // 4: statedb.StateDB.LowerBound:input_type -> statedb.QueryRequest
	4, // 5: statedb.StateDB.Watch:input_type -> statedb.WatchRequest
	1, // 6: statedb.StateDB.Meta:output_type -> statedb.MetaResponse
	6, // 7: statedb.StateDB.Get:output_type -> statedb.Object
	6, // 8: statedb.StateDB.LowerBound:output_type -> statedb.Object
	5, // 9: statedb.StateDB.Watch:output_type -> statedb.WatchResponse
	6, // [6:10] is the sub-list for method output_type
	2, // [2:6] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_statedb_proto_init() }
func file_statedb_proto_init() {
	if File_statedb_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_statedb_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*MetaRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_statedb_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*MetaResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_statedb_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Table); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_statedb_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*QueryRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_statedb_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*WatchRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_statedb_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*WatchResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_statedb_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Object); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_statedb_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   7,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_statedb_proto_goTypes,
		DependencyIndexes: file_statedb_proto_depIdxs,
		MessageInfos:      file_statedb_proto_msgTypes,
	}.Build()
	File_statedb_proto = out.File
	file_statedb_proto_rawDesc = nil
	file_statedb_proto_goTypes = nil
	file_statedb_proto_depIdxs = nil
}

package provider

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-oci/pkg/structure"
	"github.com/chainguard-dev/terraform-provider-oci/pkg/validators"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &StructureTestDataSource{}

func NewStructureTestDataSource() datasource.DataSource {
	return &StructureTestDataSource{}
}

// StructureTestDataSource defines the data source implementation.
type StructureTestDataSource struct{}

// StructureTestDataSourceModel describes the data source data model.
type StructureTestDataSourceModel struct {
	Digest     types.String `tfsdk:"digest"`
	Conditions []struct {
		Env []struct {
			Key   types.String `tfsdk:"key"`
			Value types.String `tfsdk:"value"`
		} `tfsdk:"env"`
		Files []struct {
			Path  types.String `tfsdk:"path"`
			Regex types.String `tfsdk:"regex"`
		} `tfsdk:"files"`
	} `tfsdk:"conditions"`

	Id        types.String `tfsdk:"id"`
	TestedRef types.String `tfsdk:"tested_ref"`
}

func (d *StructureTestDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_structure_test"
}

func (d *StructureTestDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Structure test data source",

		Attributes: map[string]schema.Attribute{
			"digest": schema.StringAttribute{
				MarkdownDescription: "Image digest to test",
				Optional:            false,
				Required:            true,
				Validators:          []validator.String{validators.DigestValidator{}},
			},
			"conditions": schema.ListAttribute{
				MarkdownDescription: "List of conditions to test",
				Required:            true,
				ElementType: basetypes.ObjectType{
					AttrTypes: map[string]attr.Type{
						"env": basetypes.ListType{
							ElemType: basetypes.ObjectType{
								AttrTypes: map[string]attr.Type{
									"key":   basetypes.StringType{},
									"value": basetypes.StringType{},
								},
							},
						},
						"files": basetypes.ListType{
							ElemType: basetypes.ObjectType{
								AttrTypes: map[string]attr.Type{
									"path":  basetypes.StringType{},
									"regex": basetypes.StringType{},
								},
							},
						},
					},
				},
			},

			// TODO: platform?

			"id": schema.StringAttribute{
				MarkdownDescription: "Fully qualified image digest of the image.",
				Computed:            true,
			},
			"tested_ref": schema.StringAttribute{
				MarkdownDescription: "Tested image ref by digest.",
				Computed:            true,
			},
		},
	}
}

func (d *StructureTestDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}
}

func (d *StructureTestDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data StructureTestDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ref, err := name.NewDigest(data.Digest.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid ref", fmt.Sprintf("Unable to parse ref %s, got error: %s", data.Digest.ValueString(), err))
		return
	}
	// TODO: This should accept a platform, or fail if the ref points to an index.
	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		resp.Diagnostics.AddError("Unable to fetch image", fmt.Sprintf("Unable to fetch image for ref %s, got error: %s", data.Digest.ValueString(), err))
		return
	}

	var conds structure.Conditions
	for _, c := range data.Conditions {
		for _, e := range c.Env {
			conds = append(conds, structure.EnvCondition{Want: map[string]string{
				e.Key.ValueString(): e.Value.ValueString(),
			}})
		}
		for _, f := range c.Files {
			conds = append(conds, structure.FilesCondition{Want: map[string]structure.File{
				f.Path.ValueString(): {
					Regex: f.Regex.ValueString(),
				},
			}})
		}
	}

	if err := conds.Check(img); err != nil {
		data.TestedRef = basetypes.NewStringValue("")
		data.Id = basetypes.NewStringValue("")
		resp.Diagnostics.AddError("Image does not match rules", fmt.Sprintf("Image does not match rules:\n%s", err))
		return
	}

	data.TestedRef = data.Digest
	data.Id = data.Digest

	// Write logs using the tflog package
	// Documentation: https://terraform.io/plugin/log
	tflog.Trace(ctx, "read a data source")

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

package provider

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	ggcrtypes "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &AppendResource{}
var _ resource.ResourceWithImportState = &AppendResource{}

func NewAppendResource() resource.Resource {
	return &AppendResource{}
}

// AppendResource defines the resource implementation.
type AppendResource struct{}

// AppendResourceModel describes the resource data model.
type AppendResourceModel struct {
	Id       types.String `tfsdk:"id"`
	ImageRef types.String `tfsdk:"image_ref"`

	BaseImage types.String `tfsdk:"base_image"`
	Layers    types.List   `tfsdk:"layers"`
}

func (r *AppendResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_append"
}

func (r *AppendResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Append layers to an existing image.",
		Attributes: map[string]schema.Attribute{
			"base_image": schema.StringAttribute{
				MarkdownDescription: "Base image to append layers to.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("cgr.dev/chainguard/static:latest"),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"layers": schema.ListNestedAttribute{
				MarkdownDescription: "Layers to append to the base image.",
				Optional:            false,
				Required:            true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"files": schema.MapNestedAttribute{
							MarkdownDescription: "Files to add to the layer.",
							Optional:            false,
							Required:            true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"contents": schema.StringAttribute{
										MarkdownDescription: "Content of the file.",
										Optional:            false,
										Required:            true,
									},
									// TODO: Add support for file mode.
									// TODO: Add support for symlinks.
									// TODO: Add support for deletion / whiteouts.
								},
							},
						},
					},
				},
			},
			"image_ref": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The resulting fully-qualified digest (e.g. {repo}@sha256:deadbeef).",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The resulting fully-qualified digest (e.g. {repo}@sha256:deadbeef).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *AppendResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}
}

func (r *AppendResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data *AppendResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	digest, diag := doAppend(ctx, data)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}
	data.Id = types.StringValue(digest.String())
	data.ImageRef = types.StringValue(digest.String())

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AppendResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data *AppendResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	digest, diag := doAppend(ctx, data)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	data.Id = types.StringValue(digest.String())
	data.ImageRef = types.StringValue(digest.String())

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AppendResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data *AppendResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	digest, diag := doAppend(ctx, data)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	data.Id = types.StringValue(digest.String())
	data.ImageRef = types.StringValue(digest.String())

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AppendResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data *AppendResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TODO: optionally delete the previous image when the resource is deleted.
}

func (r *AppendResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func doAppend(ctx context.Context, data *AppendResourceModel) (*name.Digest, diag.Diagnostics) {
	baseref, err := name.ParseReference(data.BaseImage.ValueString())
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to parse base image", fmt.Sprintf("Unable to parse base image %q, got error: %s", data.BaseImage.ValueString(), err))}
	}
	img, err := remote.Image(baseref,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to fetch base image", fmt.Sprintf("Unable to fetch base image %q, got error: %s", data.BaseImage.ValueString(), err))}
	}

	var ls []struct {
		Files map[string]struct {
			Contents types.String `tfsdk:"contents"`
		} `tfsdk:"files"`
	}
	if diag := data.Layers.ElementsAs(ctx, &ls, false); diag.HasError() {
		return nil, diag.Errors()
	}

	adds := []mutate.Addendum{}
	for _, l := range ls {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, f := range l.Files {
			if err := tw.WriteHeader(&tar.Header{
				Name: name,
				Size: int64(len(f.Contents.ValueString())),
			}); err != nil {
				return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to write tar header", fmt.Sprintf("Unable to write tar header for %q, got error: %s", name, err))}
			}
			if _, err := tw.Write([]byte(f.Contents.ValueString())); err != nil {
				return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to write tar contents", fmt.Sprintf("Unable to write tar contents for %q, got error: %s", name, err))}
			}
		}
		if err := tw.Close(); err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to close tar writer", fmt.Sprintf("Unable to close tar writer, got error: %s", err))}
		}

		adds = append(adds, mutate.Addendum{
			Layer:     static.NewLayer(b.Bytes(), ggcrtypes.OCILayer),
			History:   v1.History{CreatedBy: "terraform-provider-oci: oci_append"},
			MediaType: ggcrtypes.OCILayer,
		})
	}

	img, err = mutate.Append(img, adds...)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to append layers", fmt.Sprintf("Unable to append layers, got error: %s", err))}
	}
	if err := remote.Write(baseref, img,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to push image", fmt.Sprintf("Unable to push image, got error: %s", err))}
	}
	dig, err := img.Digest()
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("Unable to get image digest", fmt.Sprintf("Unable to get image digest, got error: %s", err))}
	}

	// Write logs using the tflog package
	// Documentation: https://terraform.io/plugin/log
	tflog.Trace(ctx, "created a resource")

	d := baseref.Context().Digest(dig.String())
	return &d, []diag.Diagnostic{}
}
